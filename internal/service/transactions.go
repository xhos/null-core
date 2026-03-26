package service

import (
	"context"
	"fmt"

	"null-core/internal/db/sqlc"
	"null-core/internal/exchange"
	pb "null-core/internal/gen/null/v1"

	"github.com/charmbracelet/log"
	"github.com/google/uuid"
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
	paramsList := buildCreateTxParamsList(userID, req)
	hasNoTransactions := len(paramsList) == 0
	if hasNoTransactions {
		return nil, fmt.Errorf("TransactionService.Create: no transactions provided")
	}

	for i, params := range paramsList {
		if err := validateCreateParams(params); err != nil {
			return nil, fmt.Errorf("TransactionService.Create: transaction %d invalid: %w", i, err)
		}
	}

	for i := range paramsList {
		converted, err := s.processForeignCurrency(ctx, userID, &paramsList[i])
		if err != nil {
			return nil, fmt.Errorf("TransactionService.Create: transaction %d currency conversion failed: %w", i, err)
		}
		paramsList[i] = *converted
	}

	created := make([]sqlc.Transaction, 0, len(paramsList))
	for _, params := range paramsList {
		tx, err := s.queries.CreateTransaction(ctx, params)
		if err != nil {
			return nil, wrapErr("TransactionService.Create.Insert", err)
		}
		created = append(created, tx)
	}

	affectedAccounts := make(map[int64]bool)
	for _, tx := range created {
		affectedAccounts[tx.AccountID] = true
	}
	for accountID := range affectedAccounts {
		if err := s.queries.SyncAccountBalances(ctx, accountID); err != nil {
			s.log.Warn("failed to sync account balances", "account_id", accountID, "error", err)
		}
	}

	for _, tx := range created {
		needsRules := !tx.CategoryManuallySet || !tx.MerchantManuallySet
		if needsRules {
			s.applyRulesToTransaction(ctx, userID, tx.ID)
		}
	}

	result := make([]*pb.Transaction, len(created))
	for i := range created {
		result[i] = transactionToPb(&created[i])
	}

	return result, nil
}

func (s *txnSvc) Get(ctx context.Context, userID uuid.UUID, id int64) (*pb.Transaction, error) {
	row, err := s.queries.GetTransaction(ctx, sqlc.GetTransactionParams{UserID: userID, ID: id})
	if err != nil {
		return nil, wrapErr("TransactionService.Get", err)
	}

	return transactionToPb(&row), nil
}

func (s *txnSvc) Update(ctx context.Context, userID uuid.UUID, req *pb.UpdateTransactionRequest) error {
	if err := validateUpdateRequest(req); err != nil {
		return err
	}

	params := buildUpdateTxParams(userID, req)

	tx, err := s.queries.GetTransaction(ctx, sqlc.GetTransactionParams{UserID: params.UserID, ID: params.ID})
	if err != nil {
		return wrapErr("TransactionService.Update.GetOriginal", err)
	}

	if err := s.queries.UpdateTransaction(ctx, params); err != nil {
		return wrapErr("TransactionService.Update", err)
	}

	balanceFieldsChanged := params.TxAmountCents != nil || params.TxDate != nil || params.TxDirection != nil
	accountChanged := params.AccountID != nil && *params.AccountID != tx.AccountID
	shouldSyncBalances := balanceFieldsChanged || accountChanged
	if shouldSyncBalances {
		if err := s.queries.SyncAccountBalances(ctx, tx.AccountID); err != nil {
			s.log.Warn("failed to sync original account balances after updating transaction", "tx_id", params.ID, "account_id", tx.AccountID, "error", err)
		}
		if accountChanged {
			if err := s.queries.SyncAccountBalances(ctx, *params.AccountID); err != nil {
				s.log.Warn("failed to sync new account balances after updating transaction", "tx_id", params.ID, "account_id", *params.AccountID, "error", err)
			}
		}
	}

	fieldsChangedForRules := params.TxDesc != nil || params.Merchant != nil || params.TxAmountCents != nil
	rulesCanMutate := !tx.CategoryManuallySet || !tx.MerchantManuallySet
	shouldApplyRules := fieldsChangedForRules && rulesCanMutate
	if shouldApplyRules {
		s.applyRulesToTransaction(ctx, params.UserID, params.ID)
	}

	return nil
}

func (s *txnSvc) Delete(ctx context.Context, userID uuid.UUID, ids []int64) error {
	affectedAccounts, err := s.queries.GetAccountIDsFromTransactionIDs(ctx, ids)
	if err != nil {
		return wrapErr("TransactionService.BulkDelete.GetAccounts", err)
	}

	_, err = s.queries.BulkDeleteTransactions(ctx, sqlc.BulkDeleteTransactionsParams{UserID: userID, TransactionIds: ids})
	if err != nil {
		return wrapErr("TransactionService.BulkDelete", err)
	}

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

	result := make([]*pb.Transaction, len(rows))
	for i := range rows {
		result[i] = transactionToPb(&rows[i].Transaction)
		if rows[i].ReceiptID != 0 {
			result[i].ReceiptId = &rows[i].ReceiptID
		}
	}

	var nextCursor *pb.Cursor
	hasResults := len(result) > 0
	hasLimit := req.Limit != nil
	pageIsFull := hasLimit && len(result) == int(*req.Limit)
	hasNextPage := hasResults && pageIsFull
	if hasNextPage {
		lastTx := result[len(result)-1]
		id := lastTx.Id
		nextCursor = &pb.Cursor{Date: lastTx.TxDate, Id: &id}
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
