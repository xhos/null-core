package service

import (
	"context"
	"fmt"

	"null-core/internal/db/sqlc"
	"null-core/internal/exchange"
	pb "null-core/internal/gen/null/v1"

	"github.com/charmbracelet/log"
	"github.com/google/uuid"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// ----- interface ---------------------------------------------------------------------------

type TransactionService interface {
	Create(ctx context.Context, userID uuid.UUID, req *pb.CreateTransactionRequest) ([]*pb.Transaction, error)
	Get(ctx context.Context, userID uuid.UUID, id int64) (*pb.Transaction, error)
	Update(ctx context.Context, userID uuid.UUID, req *pb.UpdateTransactionRequest) error
	Delete(ctx context.Context, userID uuid.UUID, ids []int64) error
	List(ctx context.Context, userID uuid.UUID, req *pb.ListTransactionsRequest) ([]*pb.Transaction, *pb.Cursor, error)
	Categorize(ctx context.Context, userID uuid.UUID, transactionIDs []int64, categoryID int64) error
}

type txnSvc struct {
	queries        *sqlc.Queries
	log            *log.Logger
	catSvc         CategoryService
	ruleSvc        RuleService
	exchangeClient *exchange.Client
}

func newTxnSvc(
	queries *sqlc.Queries,
	logger *log.Logger,
	catSvc CategoryService,
	ruleSvc RuleService,
	exchangeClient *exchange.Client,
) TransactionService {
	return &txnSvc{
		queries:        queries,
		log:            logger,
		catSvc:         catSvc,
		ruleSvc:        ruleSvc,
		exchangeClient: exchangeClient,
	}
}

// ----- methods -----------------------------------------------------------------------------

func (s *txnSvc) Create(ctx context.Context, userID uuid.UUID, req *pb.CreateTransactionRequest) ([]*pb.Transaction, error) {
	paramsList, err := buildCreateTxParamsList(userID, req)
	if err != nil {
		return nil, fmt.Errorf("TransactionService.Create: failed to build params: %w", err)
	}

	if len(paramsList) == 0 {
		return nil, fmt.Errorf("TransactionService.Create: no transactions provided")
	}

	// validate all transactions first
	for i, params := range paramsList {
		if err := s.validateCreateParams(params); err != nil {
			return nil, fmt.Errorf("TransactionService.Create: transaction %d invalid: %w", i, err)
		}
	}

	// process foreign currency conversions
	for i := range paramsList {
		converted, err := s.processForeignCurrency(ctx, userID, &paramsList[i])
		if err != nil {
			return nil, fmt.Errorf("TransactionService.Create: transaction %d currency conversion failed: %w", i, err)
		}
		paramsList[i] = *converted
	}

	// insert transactions
	created := make([]sqlc.Transaction, 0, len(paramsList))
	for _, params := range paramsList {
		tx, err := s.queries.CreateTransaction(ctx, params)
		if err != nil {
			return nil, wrapErr("TransactionService.Create.Insert", err)
		}
		created = append(created, tx)
	}

	// sync balances for all affected accounts (once per account)
	affectedAccounts := make(map[int64]bool)
	for _, tx := range created {
		affectedAccounts[tx.AccountID] = true
	}
	for accountID := range affectedAccounts {
		if err := s.queries.SyncAccountBalances(ctx, accountID); err != nil {
			s.log.Warn("failed to sync account balances", "account_id", accountID, "error", err)
		}
	}

	// apply rules to transactions that need it
	for _, tx := range created {
		needsRules := !tx.CategoryManuallySet || !tx.MerchantManuallySet
		if needsRules {
			s.applyRulesToTransaction(ctx, userID, tx.ID)
		}
	}

	// convert to proto
	result := make([]*pb.Transaction, len(created))
	for i := range created {
		result[i] = txToPb(&created[i])
	}

	return result, nil
}

func (s *txnSvc) Get(ctx context.Context, userID uuid.UUID, id int64) (*pb.Transaction, error) {
	row, err := s.queries.GetTransaction(ctx, sqlc.GetTransactionParams{
		UserID: userID,
		ID:     id,
	})
	if err != nil {
		return nil, wrapErr("TransactionService.Get", err)
	}

	return txToPb(&row), nil
}

func (s *txnSvc) Update(ctx context.Context, userID uuid.UUID, req *pb.UpdateTransactionRequest) error {
	params := buildUpdateTxParams(userID, req)

	tx, err := s.queries.GetTransaction(ctx, sqlc.GetTransactionParams{
		UserID: params.UserID,
		ID:     params.ID,
	})
	if err != nil {
		return wrapErr("TransactionService.Update.GetOriginal", err)
	}

	err = s.queries.UpdateTransaction(ctx, params)
	if err != nil {
		return wrapErr("TransactionService.Update", err)
	}

	// sync balances if amount, date, direction, or account changed
	balanceFieldsChanged := params.TxAmountCents != nil || params.TxDate != nil || params.TxDirection != nil
	accountChanged := params.AccountID != nil && *params.AccountID != tx.AccountID

	if balanceFieldsChanged || accountChanged {
		// sync the original account
		if err := s.queries.SyncAccountBalances(ctx, tx.AccountID); err != nil {
			s.log.Warn("failed to sync original account balances after updating transaction", "tx_id", params.ID, "account_id", tx.AccountID, "error", err)
		}

		// if account changed, also sync the new account
		if accountChanged {
			if err := s.queries.SyncAccountBalances(ctx, *params.AccountID); err != nil {
				s.log.Warn("failed to sync new account balances after updating transaction", "tx_id", params.ID, "account_id", *params.AccountID, "error", err)
			}
		}
	}

	// apply rules if relevant fields changed and aren't manually set
	fieldsChangedForRules := params.TxDesc != nil || params.Merchant != nil || params.TxAmountCents != nil
	shouldApplyRules := fieldsChangedForRules &&
		(!tx.CategoryManuallySet || !tx.MerchantManuallySet)

	if shouldApplyRules {
		s.applyRulesToTransaction(ctx, params.UserID, params.ID)
	}

	return nil
}

func (s *txnSvc) Delete(ctx context.Context, userID uuid.UUID, ids []int64) error {
	// get list of affected accounts before deletion
	affectedAccounts, err := s.queries.GetAccountIDsFromTransactionIDs(ctx, ids)
	if err != nil {
		return wrapErr("TransactionService.BulkDelete.GetAccounts", err)
	}

	_, err = s.queries.BulkDeleteTransactions(ctx, sqlc.BulkDeleteTransactionsParams{
		UserID:         userID,
		TransactionIds: ids,
	})
	if err != nil {
		return wrapErr("TransactionService.BulkDelete", err)
	}

	// sync balances for all affected accounts
	for _, accountID := range affectedAccounts {
		if err := s.queries.SyncAccountBalances(ctx, accountID); err != nil {
			s.log.Warn("failed to sync account balances after bulk delete", "account_id", accountID, "error", err)
		}
	}

	s.log.Debug("bulk deleted transactions and synced balances", "affected_accounts", len(affectedAccounts))

	return nil
}

func (s *txnSvc) List(ctx context.Context, userID uuid.UUID, req *pb.ListTransactionsRequest) ([]*pb.Transaction, *pb.Cursor, error) {
	params := buildListTxParams(userID, req)

	rows, err := s.queries.ListTransactions(ctx, params)
	if err != nil {
		return nil, nil, wrapErr("TransactionService.List", err)
	}

	// convert to proto
	result := make([]*pb.Transaction, len(rows))
	for i := range rows {
		result[i] = txToPb(&rows[i].Transaction)
		if rows[i].ReceiptID != 0 {
			result[i].ReceiptId = &rows[i].ReceiptID
		}
	}

	// build next cursor
	var nextCursor *pb.Cursor
	noMore := len(result) == 0 || req.Limit == nil || len(result) != int(*req.Limit)
	if !noMore {
		lastTx := result[len(result)-1]
		nextCursor = &pb.Cursor{
			Date: lastTx.TxDate,
			Id:   &lastTx.Id,
		}
	}

	return result, nextCursor, nil
}

func (s *txnSvc) Categorize(ctx context.Context, userID uuid.UUID, transactionIDs []int64, categoryID int64) error {
	_, err := s.queries.BulkCategorizeTransactions(ctx, sqlc.BulkCategorizeTransactionsParams{
		UserID:         userID,
		TransactionIds: transactionIDs,
		CategoryID:     categoryID,
	})
	if err != nil {
		return wrapErr("TransactionService.Categorize", err)
	}
	return nil
}

// ----- param builders ----------------------------------------------------------------------

func buildListTxParams(userID uuid.UUID, req *pb.ListTransactionsRequest) sqlc.ListTransactionsParams {
	params := sqlc.ListTransactionsParams{
		UserID: userID,
		Limit:  req.Limit,
	}

	// handle cursor pagination
	if cursor := req.GetCursor(); cursor != nil {
		if cursor.Date != nil {
			cursorTime := fromProtoTimestamp(cursor.Date)
			params.CursorDate = &cursorTime
		}
		if cursor.Id != nil {
			params.CursorID = cursor.Id
		}
	}

	// handle filters
	if req.StartDate != nil {
		startTime := fromProtoTimestamp(req.StartDate)
		params.Start = &startTime
	}
	if req.EndDate != nil {
		endTime := fromProtoTimestamp(req.EndDate)
		params.End = &endTime
	}
	if req.Direction != nil {
		direction := int16(*req.Direction)
		params.Direction = &direction
	}
	if len(req.AccountIds) > 0 {
		params.AccountIds = req.AccountIds
	}
	if len(req.Categories) > 0 {
		params.Categories = req.Categories
	}
	if req.MerchantQuery != nil {
		params.MerchantQ = req.MerchantQuery
	}
	if req.DescriptionQuery != nil {
		params.DescQ = req.DescriptionQuery
	}
	if req.Currency != nil {
		params.Currency = req.Currency
	}
	if req.Uncategorized != nil {
		params.Uncategorized = req.Uncategorized
	}

	return params
}

func buildCreateTxParamsList(userID uuid.UUID, req *pb.CreateTransactionRequest) ([]sqlc.CreateTransactionParams, error) {
	paramsList := make([]sqlc.CreateTransactionParams, len(req.Transactions))

	for i, txInput := range req.Transactions {
		txAmount := txInput.TxAmount
		txCents := moneyToCents(txAmount)
		txCurrency := txAmount.GetCurrencyCode()

		categoryManuallySet := false
		merchantManuallySet := false

		params := sqlc.CreateTransactionParams{
			UserID:              userID,
			AccountID:           txInput.GetAccountId(),
			TxDate:              fromProtoTimestamp(txInput.TxDate),
			TxAmountCents:       txCents,
			TxCurrency:          txCurrency,
			TxDirection:         int16(txInput.Direction),
			TxDesc:              txInput.Description,
			Merchant:            txInput.Merchant,
			UserNotes:           txInput.UserNotes,
			CategoryManuallySet: &categoryManuallySet,
			MerchantManuallySet: &merchantManuallySet,
		}

		if txInput.CategoryId != nil {
			params.CategoryID = txInput.CategoryId
			manuallySet := true
			params.CategoryManuallySet = &manuallySet
		}

		// if user provides merchant, mark it as manually set
		if txInput.Merchant != nil {
			manuallySet := true
			params.MerchantManuallySet = &manuallySet
		}

		if txInput.ForeignAmount != nil {
			foreignCents := moneyToCents(txInput.ForeignAmount)
			foreignCurrency := txInput.ForeignAmount.CurrencyCode
			if foreignCurrency == "" {
				foreignCurrency = "USD"
			}
			params.ForeignAmountCents = &foreignCents
			params.ForeignCurrency = &foreignCurrency
			if txInput.ExchangeRate != nil {
				params.ExchangeRate = txInput.ExchangeRate
			}
		}

		paramsList[i] = params
	}

	return paramsList, nil
}

func buildUpdateTxParams(userID uuid.UUID, req *pb.UpdateTransactionRequest) sqlc.UpdateTransactionParams {
	params := sqlc.UpdateTransactionParams{
		ID:     req.GetId(),
		UserID: userID,
	}

	if req.TxDate != nil {
		txTime := fromProtoTimestamp(req.TxDate)
		params.TxDate = &txTime
	}
	if req.TxAmount != nil {
		cents := moneyToCents(req.TxAmount)
		currency := req.TxAmount.CurrencyCode
		params.TxAmountCents = &cents
		params.TxCurrency = &currency
	}
	if req.Direction != nil {
		direction := int16(*req.Direction)
		params.TxDirection = &direction
	}
	if req.Description != nil {
		params.TxDesc = req.Description
	}
	if req.Merchant != nil {
		params.Merchant = req.Merchant
		// if setting merchant, mark as manually set; if clearing, mark as not manually set
		if *req.Merchant != "" {
			manuallySet := true
			params.MerchantManuallySet = &manuallySet
		} else {
			manuallySet := false
			params.MerchantManuallySet = &manuallySet
		}
	}
	if req.UserNotes != nil {
		params.UserNotes = req.UserNotes
	}
	if req.CategoryId != nil {
		params.CategoryID = req.CategoryId
		// if setting category, mark as manually set; if clearing, mark as not manually set
		if *req.CategoryId > 0 {
			manuallySet := true
			params.CategoryManuallySet = &manuallySet
		} else {
			manuallySet := false
			params.CategoryManuallySet = &manuallySet
		}
	}
	if req.ForeignAmount != nil {
		foreignCents := moneyToCents(req.ForeignAmount)
		foreignCurrency := req.ForeignAmount.CurrencyCode
		if foreignCurrency == "" {
			foreignCurrency = "USD"
		}
		params.ForeignAmountCents = &foreignCents
		params.ForeignCurrency = &foreignCurrency
	}
	if req.ExchangeRate != nil {
		params.ExchangeRate = req.ExchangeRate
	}
	if req.AccountId != nil {
		params.AccountID = req.AccountId
	}

	return params
}

func (s *txnSvc) GetTransactionParams(userID uuid.UUID, id int64) sqlc.GetTransactionParams {
	return sqlc.GetTransactionParams{
		UserID: userID,
		ID:     id,
	}
}

// ----- conversion helpers ------------------------------------------------------------------

func txToPb(tx *sqlc.Transaction) *pb.Transaction {
	proto := &pb.Transaction{
		Id:                  tx.ID,
		TxDate:              timestamppb.New(tx.TxDate),
		TxAmount:            centsToMoney(tx.TxAmountCents, tx.TxCurrency),
		Direction:           pb.TransactionDirection(tx.TxDirection),
		AccountId:           tx.AccountID,
		EmailId:             tx.EmailID,
		Description:         tx.TxDesc,
		CategoryId:          tx.CategoryID,
		CategoryManuallySet: tx.CategoryManuallySet,
		Merchant:            tx.Merchant,
		MerchantManuallySet: tx.MerchantManuallySet,
		UserNotes:           tx.UserNotes,
		CreatedAt:           timestamppb.New(tx.CreatedAt),
		UpdatedAt:           timestamppb.New(tx.UpdatedAt),
	}

	if tx.BalanceAfterCents != nil && tx.BalanceCurrency != nil {
		proto.BalanceAfter = centsToMoney(*tx.BalanceAfterCents, *tx.BalanceCurrency)
	}

	if tx.ForeignAmountCents != nil && tx.ForeignCurrency != nil {
		proto.ForeignAmount = centsToMoney(*tx.ForeignAmountCents, *tx.ForeignCurrency)
	}
	if tx.ExchangeRate != nil {
		proto.ExchangeRate = tx.ExchangeRate
	}

	return proto
}

// ----- internal helpers --------------------------------------------------------------------

func (s *txnSvc) validateCreateParams(params sqlc.CreateTransactionParams) error {
	if params.TxAmountCents == 0 {
		return fmt.Errorf("tx_amount cannot be zero: %w", ErrValidation)
	}

	switch params.TxDirection {
	case 1, 2:
	default:
		return fmt.Errorf("tx_direction must be 1 or 2: %w", ErrValidation)
	}

	return nil
}

func (s *txnSvc) processForeignCurrency(ctx context.Context, userID uuid.UUID, params *sqlc.CreateTransactionParams) (*sqlc.CreateTransactionParams, error) {
	account, err := s.queries.GetAccount(ctx, sqlc.GetAccountParams{
		UserID: userID,
		ID:     params.AccountID,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to fetch account: %w", err)
	}

	if params.TxCurrency == account.Account.AnchorCurrency {
		params.ForeignAmountCents = nil
		params.ForeignCurrency = nil
		params.ExchangeRate = nil
		return params, nil
	}

	foreignAmountCents := params.TxAmountCents
	foreignCurrency := params.TxCurrency

	rate, err := s.exchangeClient.GetExchangeRate(foreignCurrency, account.Account.AnchorCurrency, &params.TxDate)
	if err != nil {
		return nil, fmt.Errorf("failed to get exchange rate from %s to %s: %w", foreignCurrency, account.Account.AnchorCurrency, err)
	}

	params.TxAmountCents = int64(float64(foreignAmountCents) * rate)
	params.TxCurrency = account.Account.AnchorCurrency
	params.ForeignAmountCents = &foreignAmountCents
	params.ForeignCurrency = &foreignCurrency
	params.ExchangeRate = &rate

	return params, nil
}

func (s *txnSvc) applyRulesToTransaction(ctx context.Context, userID uuid.UUID, txID int64) {
	tx, err := s.queries.GetTransaction(ctx, sqlc.GetTransactionParams{
		UserID: userID,
		ID:     txID,
	})
	if err != nil {
		s.log.Warn("failed to fetch transaction for rule application", "tx_id", txID, "error", err)
		return
	}

	bothManuallySet := tx.CategoryManuallySet && tx.MerchantManuallySet
	if bothManuallySet {
		return
	}

	account, err := s.queries.GetAccount(ctx, sqlc.GetAccountParams{
		UserID: userID,
		ID:     tx.AccountID,
	})
	if err != nil {
		s.log.Warn("failed to fetch account for rule application", "account_id", tx.AccountID, "error", err)
		return
	}

	result, err := s.ruleSvc.ApplyToTransaction(ctx, userID, &tx, &account)
	if err != nil {
		s.log.Warn("failed to apply rules", "tx_id", txID, "error", err)
		return
	}

	noMatch := result.CategoryID == nil && result.Merchant == nil
	if noMatch {
		return
	}

	updateParams := sqlc.UpdateTransactionParams{
		ID:     txID,
		UserID: userID,
	}

	if !tx.CategoryManuallySet && result.CategoryID != nil {
		updateParams.CategoryID = result.CategoryID
	}

	if !tx.MerchantManuallySet && result.Merchant != nil {
		updateParams.Merchant = result.Merchant
	}

	if err := s.queries.UpdateTransaction(ctx, updateParams); err != nil {
		s.log.Warn("failed to update transaction with rule results", "tx_id", txID, "error", err)
	}
}
