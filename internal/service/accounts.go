package service

import (
	"context"
	"fmt"
	"strings"

	"null-core/internal/db/sqlc"
	pb "null-core/internal/gen/null/v1"

	"github.com/charmbracelet/log"
	"github.com/google/uuid"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// ----- interface --------------------------------------------------------------------------------

type AccountService interface {
	Create(ctx context.Context, req *pb.CreateAccountRequest) (*pb.Account, error)
	Get(ctx context.Context, userID uuid.UUID, accountID int64) (*pb.Account, error)
	Update(ctx context.Context, userID uuid.UUID, req *pb.UpdateAccountRequest) error
	Delete(ctx context.Context, userID uuid.UUID, accountID int64) (int64, error)
	List(ctx context.Context, userID uuid.UUID) ([]*pb.Account, error)

	AddAlias(ctx context.Context, userID uuid.UUID, accountID int64, alias string) error
	RemoveAlias(ctx context.Context, userID uuid.UUID, accountID int64, alias string) error
	SetAliases(ctx context.Context, userID uuid.UUID, accountID int64, aliases []string) error
	FindByAlias(ctx context.Context, userID uuid.UUID, alias string) (*pb.Account, error)

	MergeAccounts(ctx context.Context, userID uuid.UUID, primaryID, secondaryID int64) (*pb.Account, int64, error)
}

type acctSvc struct {
	queries *sqlc.Queries
	log     *log.Logger
}

func newAcctSvc(queries *sqlc.Queries, logger *log.Logger) AccountService {
	return &acctSvc{queries: queries, log: logger}
}

// ----- methods ----------------------------------------------------------------------------------

func (s *acctSvc) Create(ctx context.Context, req *pb.CreateAccountRequest) (*pb.Account, error) {
	userID, err := uuid.Parse(req.GetUserId())
	if err != nil {
		return nil, wrapErr("AccountService.Create", fmt.Errorf("invalid user_id: %w", err))
	}

	anchorBalance := req.GetAnchorBalance()
	anchorCents := moneyToCents(anchorBalance)
	anchorCurrency := anchorBalance.GetCurrencyCode()

	mainCurrency := req.GetMainCurrency()

	colors := req.GetColors()
	if len(colors) == 0 {
		colors = []string{"#1f2937", "#3b82f6", "#10b981"}
	} else if len(colors) != 3 {
		return nil, wrapErr(
			"AccountService.Create",
			fmt.Errorf("colors must be exactly 3 hex values, got %d", len(colors)))
	}

	params := sqlc.CreateAccountParams{
		OwnerID:            userID,
		Name:               req.GetName(),
		Bank:               req.GetBank(),
		AccountType:        int16(req.GetType()),
		FriendlyName:       req.FriendlyName,
		AnchorBalanceCents: anchorCents,
		AnchorCurrency:     anchorCurrency,
		MainCurrency:       mainCurrency,
		Colors:             colors,
	}

	created, err := s.queries.CreateAccount(ctx, params)
	if err != nil {
		return nil, wrapErr("AccountService.Create", err)
	}

	return accountRowToPb(created, created.AnchorBalanceCents, created.AnchorCurrency), nil
}

func (s *acctSvc) Get(ctx context.Context, userID uuid.UUID, accountID int64) (*pb.Account, error) {
	row, err := s.queries.GetAccount(ctx, sqlc.GetAccountParams{
		UserID: userID,
		ID:     accountID,
	})
	if err != nil {
		return nil, wrapErr("AccountService.Get", err)
	}

	return accountRowToPb(row.Account, row.BalanceCents, row.BalanceCurrency), nil
}

func (s *acctSvc) Update(ctx context.Context, userID uuid.UUID, req *pb.UpdateAccountRequest) error {
	params := sqlc.UpdateAccountParams{
		ID:     req.GetId(),
		UserID: userID,
	}

	if req.Name != nil {
		params.Name = req.Name
	}
	if req.Bank != nil {
		params.Bank = req.Bank
	}
	if req.AccountType != nil {
		accountType := int16(*req.AccountType)
		params.AccountType = &accountType
	}
	if req.FriendlyName != nil {
		params.FriendlyName = req.FriendlyName
	}
	if req.AnchorDate != nil {
		t := req.AnchorDate.AsTime()
		params.AnchorDate = &t
	}
	if req.AnchorBalance != nil {
		cents := moneyToCents(req.AnchorBalance)
		currency := req.AnchorBalance.CurrencyCode
		params.AnchorBalanceCents = &cents
		params.AnchorCurrency = &currency
	}
	if req.MainCurrency != nil {
		params.MainCurrency = req.MainCurrency
	}
	if len(req.Colors) > 0 {
		params.Colors = req.Colors
	}

	err := s.queries.UpdateAccount(ctx, params)
	if err != nil {
		return wrapErr("AccountService.Update", err)
	}

	anchorFieldsChanged := params.AnchorDate != nil || params.AnchorBalanceCents != nil
	if anchorFieldsChanged {
		if err := s.queries.SyncAccountBalances(ctx, params.ID); err != nil {
			s.log.Warn("failed to sync account balances after updating anchor", "account_id", params.ID, "error", err)
		}
	}

	return nil
}

func (s *acctSvc) Delete(ctx context.Context, userID uuid.UUID, id int64) (int64, error) {
	affected, err := s.queries.DeleteAccount(ctx, sqlc.DeleteAccountParams{
		UserID: userID,
		ID:     id,
	})
	if err != nil {
		return 0, wrapErr("AccountService.Delete", err)
	}
	return affected, nil
}

func (s *acctSvc) List(ctx context.Context, userID uuid.UUID) ([]*pb.Account, error) {
	rows, err := s.queries.ListAccounts(ctx, userID)
	if err != nil {
		return nil, wrapErr("AccountService.List", err)
	}

	accounts := make([]*pb.Account, len(rows))
	for i, row := range rows {
		accounts[i] = accountRowToPb(row.Account, row.BalanceCents, row.BalanceCurrency)
	}

	return accounts, nil
}

func (s *acctSvc) AddAlias(ctx context.Context, userID uuid.UUID, accountID int64, alias string) error {
	alias = strings.TrimSpace(alias)
	if alias == "" {
		return wrapErr("AccountService.AddAlias", fmt.Errorf("%w: alias cannot be empty", ErrValidation))
	}

	if err := s.checkAliasConflict(ctx, userID, accountID, alias); err != nil {
		return wrapErr("AccountService.AddAlias", err)
	}

	if err := s.queries.AddAccountAlias(ctx, sqlc.AddAccountAliasParams{
		ID:     accountID,
		UserID: userID,
		Alias:  alias,
	}); err != nil {
		return wrapErr("AccountService.AddAlias", err)
	}
	return nil
}

func (s *acctSvc) RemoveAlias(ctx context.Context, userID uuid.UUID, accountID int64, alias string) error {
	alias = strings.TrimSpace(alias)
	if alias == "" {
		return wrapErr("AccountService.RemoveAlias", fmt.Errorf("%w: alias cannot be empty", ErrValidation))
	}

	if err := s.queries.RemoveAccountAlias(ctx, sqlc.RemoveAccountAliasParams{
		ID:     accountID,
		UserID: userID,
		Alias:  alias,
	}); err != nil {
		return wrapErr("AccountService.RemoveAlias", err)
	}
	return nil
}

func (s *acctSvc) SetAliases(ctx context.Context, userID uuid.UUID, accountID int64, aliases []string) error {
	seen := make(map[string]struct{}, len(aliases))
	cleaned := make([]string, 0, len(aliases))
	for _, a := range aliases {
		a = strings.TrimSpace(a)
		if a == "" {
			return wrapErr("AccountService.SetAliases", fmt.Errorf("%w: alias cannot be empty", ErrValidation))
		}
		if _, dup := seen[a]; dup {
			continue
		}
		seen[a] = struct{}{}
		cleaned = append(cleaned, a)
	}

	for _, a := range cleaned {
		if err := s.checkAliasConflict(ctx, userID, accountID, a); err != nil {
			return wrapErr("AccountService.SetAliases", err)
		}
	}

	if err := s.queries.SetAccountAliases(ctx, sqlc.SetAccountAliasesParams{
		ID:      accountID,
		UserID:  userID,
		Aliases: cleaned,
	}); err != nil {
		return wrapErr("AccountService.SetAliases", err)
	}
	return nil
}

func (s *acctSvc) FindByAlias(ctx context.Context, userID uuid.UUID, alias string) (*pb.Account, error) {
	alias = strings.TrimSpace(alias)
	if alias == "" {
		return nil, wrapErr("AccountService.FindByAlias", fmt.Errorf("%w: alias cannot be empty", ErrValidation))
	}

	row, err := s.queries.FindAccountByAlias(ctx, sqlc.FindAccountByAliasParams{
		UserID: userID,
		Alias:  alias,
	})
	if err != nil {
		return nil, wrapErr("AccountService.FindByAlias", err)
	}

	return accountRowToPb(row.Account, row.Account.AnchorBalanceCents, row.Account.AnchorCurrency), nil
}

func (s *acctSvc) checkAliasConflict(ctx context.Context, userID uuid.UUID, accountID int64, alias string) error {
	if existing, err := s.queries.FindAccountByAlias(ctx, sqlc.FindAccountByAliasParams{
		UserID: userID,
		Alias:  alias,
	}); err == nil && existing.Account.ID != accountID {
		return fmt.Errorf("%w: alias %q is already used by account %d", ErrValidation, alias, existing.Account.ID)
	}

	if existing, err := s.queries.FindAccountByName(ctx, sqlc.FindAccountByNameParams{
		UserID: userID,
		Name:   alias,
	}); err == nil && existing.Account.ID != accountID {
		return fmt.Errorf("%w: alias %q conflicts with account name %d", ErrValidation, alias, existing.Account.ID)
	}

	return nil
}

func (s *acctSvc) MergeAccounts(ctx context.Context, userID uuid.UUID, primaryID, secondaryID int64) (*pb.Account, int64, error) {
	if primaryID == secondaryID {
		return nil, 0, wrapErr("AccountService.MergeAccounts", fmt.Errorf("%w: primary and secondary accounts must be different", ErrValidation))
	}

	primary, err := s.queries.GetAccount(ctx, sqlc.GetAccountParams{UserID: userID, ID: primaryID})
	if err != nil {
		return nil, 0, wrapErr("AccountService.MergeAccounts", fmt.Errorf("primary account: %w", err))
	}

	secondary, err := s.queries.GetAccount(ctx, sqlc.GetAccountParams{UserID: userID, ID: secondaryID})
	if err != nil {
		return nil, 0, wrapErr("AccountService.MergeAccounts", fmt.Errorf("secondary account: %w", err))
	}

	moved, err := s.queries.MoveAccountTransactions(ctx, sqlc.MoveAccountTransactionsParams{
		PrimaryID:   primaryID,
		SecondaryID: secondaryID,
	})
	if err != nil {
		return nil, 0, wrapErr("AccountService.MergeAccounts", fmt.Errorf("moving transactions: %w", err))
	}

	// Build merged alias list: existing primary aliases + secondary name + secondary aliases
	seen := make(map[string]struct{})
	merged := make([]string, 0)
	for _, a := range primary.Account.Aliases {
		if _, ok := seen[a]; !ok {
			seen[a] = struct{}{}
			merged = append(merged, a)
		}
	}
	for _, candidate := range append([]string{secondary.Account.Name}, secondary.Account.Aliases...) {
		candidate = strings.TrimSpace(candidate)
		if candidate == "" {
			continue
		}
		if _, ok := seen[candidate]; !ok {
			seen[candidate] = struct{}{}
			merged = append(merged, candidate)
		}
	}

	// Set aliases directly, bypassing conflict checks (secondary is about to be deleted)
	if err := s.queries.SetAccountAliases(ctx, sqlc.SetAccountAliasesParams{
		ID:      primaryID,
		UserID:  userID,
		Aliases: merged,
	}); err != nil {
		return nil, 0, wrapErr("AccountService.MergeAccounts", fmt.Errorf("updating aliases: %w", err))
	}

	if err := s.queries.SyncAccountBalances(ctx, primaryID); err != nil {
		s.log.Warn("failed to sync balances after merge", "account_id", primaryID, "error", err)
	}

	if _, err := s.queries.DeleteAccount(ctx, sqlc.DeleteAccountParams{UserID: userID, ID: secondaryID}); err != nil {
		return nil, 0, wrapErr("AccountService.MergeAccounts", fmt.Errorf("deleting secondary: %w", err))
	}

	result, err := s.queries.GetAccount(ctx, sqlc.GetAccountParams{UserID: userID, ID: primaryID})
	if err != nil {
		return nil, 0, wrapErr("AccountService.MergeAccounts", err)
	}

	return accountRowToPb(result.Account, result.BalanceCents, result.BalanceCurrency), moved, nil
}

// ----- conversion helpers -----------------------------------------------------------------------

func accountRowToPb(a sqlc.Account, balanceCents int64, balanceCurrency string) *pb.Account {
	return &pb.Account{
		Id:            a.ID,
		OwnerId:       a.OwnerID.String(),
		Name:          a.Name,
		Bank:          a.Bank,
		Type:          pb.AccountType(a.AccountType),
		FriendlyName:  a.FriendlyName,
		AnchorDate:    timestamppb.New(a.AnchorDate),
		AnchorBalance: centsToMoney(a.AnchorBalanceCents, a.AnchorCurrency),
		MainCurrency:  a.MainCurrency,
		Colors:        a.Colors,
		CreatedAt:     timestamppb.New(a.CreatedAt),
		UpdatedAt:     timestamppb.New(a.UpdatedAt),
		Balance:       centsToMoney(balanceCents, balanceCurrency),
	}
}
