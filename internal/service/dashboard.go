package service

import (
	"context"
	"fmt"
	"time"

	"null-core/internal/db/sqlc"
	pb "null-core/internal/gen/null/v1"

	"github.com/google/uuid"
	"google.golang.org/genproto/googleapis/type/money"
)

// ----- types -------------------------------------------------------------------------------

type AccountSummary struct {
	Summary *sqlc.GetDashboardSummaryRow
	Trends  []sqlc.GetDashboardTrendsRow
}

type PeriodType int

const (
	Period7Days PeriodType = iota
	Period30Days
	Period90Days
	PeriodCustom
	Period3Months
	Period6Months
	Period1Year
	PeriodAllTime
)

type CategorySpendingParams struct {
	UserID      uuid.UUID
	PeriodType  PeriodType
	CustomStart *time.Time
	CustomEnd   *time.Time
}

type PeriodInfo struct {
	StartDate string
	EndDate   string
	Label     string
}

type CategorySpendingResult struct {
	CurrentPeriod  PeriodInfo
	PreviousPeriod PeriodInfo
	Current        []sqlc.GetTopCategoriesRow
	Previous       []sqlc.GetTopCategoriesRow
}

type NetWorthHistoryParams struct {
	UserID      uuid.UUID
	StartDate   time.Time
	EndDate     time.Time
	Granularity int32
}

// ----- interface ---------------------------------------------------------------------------

type DashboardService interface {
	Balance(ctx context.Context, userID uuid.UUID) (*money.Money, error)
	Debt(ctx context.Context, userID uuid.UUID) (*money.Money, error)
	NetBalance(ctx context.Context, userID uuid.UUID) (*money.Money, error)
	Trends(ctx context.Context, userID uuid.UUID, req *pb.GetSpendingTrendsRequest) ([]*pb.TrendPoint, error)
	Summary(ctx context.Context, userID uuid.UUID, req *pb.GetDashboardSummaryRequest) (*pb.DashboardSummary, error)
	MonthlyComparison(ctx context.Context, userID uuid.UUID, monthsBack int32) ([]*pb.MonthlyComparison, error)
	TopCategories(ctx context.Context, userID uuid.UUID, req *pb.GetTopCategoriesRequest) ([]*pb.TopCategory, error)
	TopMerchants(ctx context.Context, userID uuid.UUID, req *pb.GetTopMerchantsRequest) ([]*pb.TopMerchant, error)
	AccountBalances(ctx context.Context, userID uuid.UUID) ([]*pb.AccountBalance, error)
	GetSpendingTrends(ctx context.Context, userID uuid.UUID, startDate string, endDate string, categoryID *int64, accountID *int64) ([]*pb.TrendPoint, error)
	GetCategorySpendingComparison(ctx context.Context, params CategorySpendingParams) (*CategorySpendingResult, error)
	GetNetWorthHistory(ctx context.Context, params NetWorthHistoryParams) ([]*pb.NetWorthPoint, error)
	GetEarliestTransactionDate(ctx context.Context, userID uuid.UUID) (time.Time, error)
}

type dashSvc struct {
	queries *sqlc.Queries
}

func newDashSvc(queries *sqlc.Queries) DashboardService {
	return &dashSvc{queries: queries}
}

// ----- methods -----------------------------------------------------------------------------

func (s *dashSvc) Balance(ctx context.Context, userID uuid.UUID) (*money.Money, error) {
	balances, err := s.queries.GetAccountBalances(ctx, userID)
	if err != nil {
		return nil, wrapErr("DashboardService.Balance", err)
	}

	totalCents := int64(0)
	for _, balance := range balances {
		if balance.BalanceCents > 0 {
			totalCents += balance.BalanceCents
		}
	}

	return s.centsToMoney(ctx, userID, totalCents)
}

func (s *dashSvc) Debt(ctx context.Context, userID uuid.UUID) (*money.Money, error) {
	balances, err := s.queries.GetAccountBalances(ctx, userID)
	if err != nil {
		return nil, wrapErr("DashboardService.Debt", err)
	}

	totalCents := int64(0)
	for _, balance := range balances {
		if balance.BalanceCents < 0 {
			totalCents += -balance.BalanceCents
		}
	}

	return s.centsToMoney(ctx, userID, totalCents)
}

func (s *dashSvc) Trends(ctx context.Context, userID uuid.UUID, req *pb.GetSpendingTrendsRequest) ([]*pb.TrendPoint, error) {
	startTime := dateToTime(req.StartDate)
	endTime := dateToTime(req.EndDate)

	params := sqlc.GetDashboardTrendsParams{
		UserID: userID,
		Start:  startTime,
		End:    endTime,
	}

	trends, err := s.queries.GetDashboardTrends(ctx, params)
	if err != nil {
		return nil, wrapErr("DashboardService.Trends", err)
	}

	result := make([]*pb.TrendPoint, len(trends))
	for i, trend := range trends {
		result[i] = trendPointToPb(&trend)
	}
	return result, nil
}

func (s *dashSvc) Summary(ctx context.Context, userID uuid.UUID, req *pb.GetDashboardSummaryRequest) (*pb.DashboardSummary, error) {
	params := buildDashboardSummaryParams(userID, req)
	summary, err := s.queries.GetDashboardSummary(ctx, params)
	if err != nil {
		return nil, wrapErr("DashboardService.Summary", err)
	}
	return dashboardSummaryToPb(&summary), nil
}

func (s *dashSvc) MonthlyComparison(ctx context.Context, userID uuid.UUID, monthsBack int32) ([]*pb.MonthlyComparison, error) {
	params := buildMonthlyComparisonParams(userID, monthsBack)
	comparison, err := s.queries.GetMonthlyComparison(ctx, params)
	if err != nil {
		return nil, wrapErr("DashboardService.MonthlyComparison", err)
	}

	result := make([]*pb.MonthlyComparison, len(comparison))
	for i, comp := range comparison {
		result[i] = monthlyComparisonToPb(&comp)
	}
	return result, nil
}

func (s *dashSvc) TopCategories(ctx context.Context, userID uuid.UUID, req *pb.GetTopCategoriesRequest) ([]*pb.TopCategory, error) {
	params := buildTopCategoriesParams(userID, req)
	categories, err := s.queries.GetTopCategories(ctx, params)
	if err != nil {
		return nil, wrapErr("DashboardService.TopCategories", err)
	}

	result := make([]*pb.TopCategory, len(categories))
	for i, cat := range categories {
		result[i] = topCategoryToPb(&cat)
	}
	return result, nil
}

func (s *dashSvc) TopMerchants(ctx context.Context, userID uuid.UUID, req *pb.GetTopMerchantsRequest) ([]*pb.TopMerchant, error) {
	params := buildTopMerchantsParams(userID, req)
	merchants, err := s.queries.GetTopMerchants(ctx, params)
	if err != nil {
		return nil, wrapErr("DashboardService.TopMerchants", err)
	}

	result := make([]*pb.TopMerchant, len(merchants))
	for i, merchant := range merchants {
		result[i] = topMerchantToPb(&merchant)
	}
	return result, nil
}

func (s *dashSvc) NetBalance(ctx context.Context, userID uuid.UUID) (*money.Money, error) {
	balances, err := s.queries.GetAccountBalances(ctx, userID)
	if err != nil {
		return nil, wrapErr("DashboardService.NetBalance", err)
	}

	totalCents := int64(0)
	for _, balance := range balances {
		totalCents += balance.BalanceCents
	}

	return s.centsToMoney(ctx, userID, totalCents)
}

func (s *dashSvc) AccountBalances(ctx context.Context, userID uuid.UUID) ([]*pb.AccountBalance, error) {
	balances, err := s.queries.GetAccountBalances(ctx, userID)
	if err != nil {
		return nil, wrapErr("DashboardService.AccountBalances", err)
	}

	result := make([]*pb.AccountBalance, len(balances))
	for i, balance := range balances {
		result[i] = accountBalanceToPb(&balance)
	}
	return result, nil
}

func (s *dashSvc) GetSpendingTrends(ctx context.Context, userID uuid.UUID, startDate string, endDate string, categoryID *int64, accountID *int64) ([]*pb.TrendPoint, error) {
	parsedStart, err := time.Parse("2006-01-02", startDate)
	if err != nil {
		return nil, wrapErr("DashboardService.GetSpendingTrends.ParseStartDate", err)
	}

	parsedEnd, err := time.Parse("2006-01-02", endDate)
	if err != nil {
		return nil, wrapErr("DashboardService.GetSpendingTrends.ParseEndDate", err)
	}

	// TODO: categoryID and accountID ignored until query supports filtering
	_ = categoryID
	_ = accountID

	req := &pb.GetSpendingTrendsRequest{
		StartDate: timeToDate(parsedStart),
		EndDate:   timeToDate(parsedEnd),
	}

	return s.Trends(ctx, userID, req)
}

func (s *dashSvc) GetCategorySpendingComparison(
	ctx context.Context,
	params CategorySpendingParams,
) (*CategorySpendingResult, error) {
	loc := s.getUserLocation(ctx, params.UserID)
	now := time.Now().In(loc)

	// Get earliest transaction date for all-time period
	var earliestTxDate *time.Time
	if params.PeriodType == PeriodAllTime {
		date, err := s.queries.GetEarliestTransactionDate(ctx, params.UserID)
		if err == nil {
			earliestTxDate = &date
		}
	}

	periods, err := s.calculatePeriods(params, now, loc, earliestTxDate)
	if err != nil {
		return nil, err
	}

	current, err := s.queries.GetTopCategories(ctx, sqlc.GetTopCategoriesParams{
		UserID: params.UserID,
		Start:  &periods.currentStart,
		End:    &periods.currentEnd,
		Limit:  int32Ptr(20),
	})
	if err != nil {
		return nil, wrapErr("DashboardService.GetCategorySpendingComparison.Current", err)
	}

	previous, err := s.queries.GetTopCategories(ctx, sqlc.GetTopCategoriesParams{
		UserID: params.UserID,
		Start:  &periods.previousStart,
		End:    &periods.previousEnd,
		Limit:  int32Ptr(20),
	})
	if err != nil {
		return nil, wrapErr("DashboardService.GetCategorySpendingComparison.Previous", err)
	}

	return &CategorySpendingResult{
		CurrentPeriod: PeriodInfo{
			StartDate: periods.currentStart.Format("2006-01-02"),
			EndDate:   periods.currentEnd.Format("2006-01-02"),
			Label:     periods.currentLabel,
		},
		PreviousPeriod: PeriodInfo{
			StartDate: periods.previousStart.Format("2006-01-02"),
			EndDate:   periods.previousEnd.Format("2006-01-02"),
			Label:     periods.previousLabel,
		},
		Current:  current,
		Previous: previous,
	}, nil
}

type periodBounds struct {
	currentStart, currentEnd    time.Time
	previousStart, previousEnd  time.Time
	currentLabel, previousLabel string
}

func (s *dashSvc) calculatePeriods(params CategorySpendingParams, now time.Time, loc *time.Location, earliestTxDate *time.Time) (*periodBounds, error) {
	p := &periodBounds{}

	switch params.PeriodType {
	case Period7Days:
		p.currentStart = startOfDay(now.AddDate(0, 0, -6), loc)
		p.currentEnd = endOfDay(now, loc)
		p.previousEnd = startOfDay(p.currentStart, loc).Add(-time.Nanosecond)
		p.previousStart = startOfDay(now.AddDate(0, 0, -13), loc)
		p.currentLabel = "Last 7 Days"
		p.previousLabel = "Previous 7 Days"

	case Period30Days:
		p.currentStart = startOfDay(now.AddDate(0, 0, -29), loc)
		p.currentEnd = endOfDay(now, loc)
		p.previousEnd = startOfDay(p.currentStart, loc).Add(-time.Nanosecond)
		p.previousStart = startOfDay(now.AddDate(0, 0, -59), loc)
		p.currentLabel = "Last 30 Days"
		p.previousLabel = "Previous 30 Days"

	case Period90Days:
		p.currentStart = startOfDay(now.AddDate(0, 0, -89), loc)
		p.currentEnd = endOfDay(now, loc)
		p.previousEnd = startOfDay(p.currentStart, loc).Add(-time.Nanosecond)
		p.previousStart = startOfDay(now.AddDate(0, 0, -179), loc)
		p.currentLabel = "Last 90 Days"
		p.previousLabel = "Previous 90 Days"

	case Period3Months:
		p.currentStart = startOfDay(now.AddDate(0, -3, 0), loc)
		p.currentEnd = endOfDay(now, loc)
		p.previousEnd = startOfDay(p.currentStart, loc).Add(-time.Nanosecond)
		p.previousStart = startOfDay(now.AddDate(0, -6, 0), loc)
		p.currentLabel = "Last 3 Months"
		p.previousLabel = "Previous 3 Months"

	case Period6Months:
		p.currentStart = startOfDay(now.AddDate(0, -6, 0), loc)
		p.currentEnd = endOfDay(now, loc)
		p.previousEnd = startOfDay(p.currentStart, loc).Add(-time.Nanosecond)
		p.previousStart = startOfDay(now.AddDate(0, -12, 0), loc)
		p.currentLabel = "Last 6 Months"
		p.previousLabel = "Previous 6 Months"

	case Period1Year:
		p.currentStart = startOfDay(now.AddDate(-1, 0, 0), loc)
		p.currentEnd = endOfDay(now, loc)
		p.previousEnd = startOfDay(p.currentStart, loc).Add(-time.Nanosecond)
		p.previousStart = startOfDay(now.AddDate(-2, 0, 0), loc)
		p.currentLabel = "Last Year"
		p.previousLabel = "Previous Year"

	case PeriodAllTime:
		// For all-time, use the earliest transaction date if available
		if earliestTxDate != nil {
			p.currentStart = startOfDay(*earliestTxDate, loc)
		} else {
			// Fallback to 1 year if no transactions
			p.currentStart = startOfDay(now.AddDate(-1, 0, 0), loc)
		}
		p.currentEnd = endOfDay(now, loc)
		// For all-time, calculate a matching previous period (same duration before start)
		duration := p.currentEnd.Sub(p.currentStart)
		p.previousEnd = startOfDay(p.currentStart, loc).Add(-time.Nanosecond)
		p.previousStart = p.previousEnd.Add(-duration)
		p.currentLabel = "All Time"
		p.previousLabel = "Previous Period"

	case PeriodCustom:
		if params.CustomStart == nil || params.CustomEnd == nil {
			return nil, wrapErr("DashboardService.GetCategorySpendingComparison",
				fmt.Errorf("custom period requires both start and end dates"))
		}

		p.currentStart = startOfDay(*params.CustomStart, loc)
		p.currentEnd = endOfDay(*params.CustomEnd, loc)

		if p.currentEnd.Before(p.currentStart) {
			return nil, wrapErr("DashboardService.GetCategorySpendingComparison",
				fmt.Errorf("end date must be after start date"))
		}

		duration := p.currentEnd.Sub(p.currentStart) + time.Nanosecond
		p.previousEnd = startOfDay(p.currentStart, loc).Add(-time.Nanosecond)
		p.previousStart = p.currentStart.Add(-duration)
		p.currentLabel = formatDateRange(p.currentStart, p.currentEnd)
		p.previousLabel = formatDateRange(p.previousStart, p.previousEnd)

	default:
		return nil, wrapErr("DashboardService.GetCategorySpendingComparison",
			fmt.Errorf("invalid period type"))
	}

	return p, nil
}

// getUserPrimaryCurrency retrieves the user's primary currency
// Falls back to CAD if not found or on error
func (s *dashSvc) getUserPrimaryCurrency(ctx context.Context, userID uuid.UUID) string {
	currency, err := s.queries.GetUserPrimaryCurrency(ctx, userID)
	if err != nil {
		return "CAD"
	}
	return currency
}

func (s *dashSvc) getUserLocation(ctx context.Context, userID uuid.UUID) *time.Location {
	timezone, err := s.queries.GetUserTimezone(ctx, userID)
	if err != nil {
		return time.UTC
	}
	loc, err := time.LoadLocation(timezone)
	if err != nil {
		return time.UTC
	}
	return loc
}

func (s *dashSvc) centsToMoney(ctx context.Context, userID uuid.UUID, cents int64) (*money.Money, error) {
	currency := s.getUserPrimaryCurrency(ctx, userID)
	return &money.Money{
		CurrencyCode: currency,
		Units:        cents / 100,
		Nanos:        int32((cents % 100) * 10_000_000),
	}, nil
}

func (s *dashSvc) GetNetWorthHistory(
	ctx context.Context,
	params NetWorthHistoryParams,
) ([]*pb.NetWorthPoint, error) {
	loc := s.getUserLocation(ctx, params.UserID)

	result, err := s.queries.GetNetWorthHistory(ctx, sqlc.GetNetWorthHistoryParams{
		UserID:      params.UserID,
		StartDate:   params.StartDate.In(loc),
		EndDate:     params.EndDate.In(loc),
		Granularity: params.Granularity,
	})
	if err != nil {
		return nil, wrapErr("DashboardService.GetNetWorthHistory", err)
	}

	// Get user's primary currency
	currency := s.getUserPrimaryCurrency(ctx, params.UserID)

	protoResult := make([]*pb.NetWorthPoint, len(result))
	for i, row := range result {
		protoResult[i] = netWorthPointToPb(&row, currency)
	}

	return protoResult, nil
}

func (s *dashSvc) GetEarliestTransactionDate(ctx context.Context, userID uuid.UUID) (time.Time, error) {
	date, err := s.queries.GetEarliestTransactionDate(ctx, userID)
	if err != nil {
		return time.Time{}, wrapErr("DashboardService.GetEarliestTransactionDate", err)
	}
	return date, nil
}

// ----- param builders ----------------------------------------------------------------------

func buildDashboardSummaryParams(userID uuid.UUID, req *pb.GetDashboardSummaryRequest) sqlc.GetDashboardSummaryParams {
	return sqlc.GetDashboardSummaryParams{
		UserID: userID,
		Start:  dateToTime(req.StartDate),
		End:    dateToTime(req.EndDate),
	}
}

func buildTopCategoriesParams(userID uuid.UUID, req *pb.GetTopCategoriesRequest) sqlc.GetTopCategoriesParams {
	return sqlc.GetTopCategoriesParams{
		UserID: userID,
		Start:  dateToTime(req.StartDate),
		End:    dateToTime(req.EndDate),
		Limit:  req.Limit,
	}
}

func buildTopMerchantsParams(userID uuid.UUID, req *pb.GetTopMerchantsRequest) sqlc.GetTopMerchantsParams {
	return sqlc.GetTopMerchantsParams{
		UserID: userID,
		Start:  dateToTime(req.StartDate),
		End:    dateToTime(req.EndDate),
		Limit:  req.Limit,
	}
}

func buildMonthlyComparisonParams(userID uuid.UUID, monthsBack int32) sqlc.GetMonthlyComparisonParams {
	end := time.Now()
	start := end.AddDate(0, -int(monthsBack), 0)

	return sqlc.GetMonthlyComparisonParams{
		UserID: userID,
		Start:  &start,
		End:    &end,
	}
}

// ----- conversion helpers ------------------------------------------------------------------

func trendPointToPb(trend *sqlc.GetDashboardTrendsRow) *pb.TrendPoint {
	if trend == nil {
		return nil
	}

	// parse the date string to time.Time for conversion
	trendDate, _ := time.Parse("2006-01-02", trend.Date)
	return &pb.TrendPoint{
		Date:     timeToDate(trendDate),
		Income:   centsToMoney(trend.IncomeCents, "CAD"),
		Expenses: centsToMoney(trend.ExpenseCents, "CAD"),
	}
}

func monthlyComparisonToPb(comp *sqlc.GetMonthlyComparisonRow) *pb.MonthlyComparison {
	if comp == nil {
		return nil
	}

	return &pb.MonthlyComparison{
		Month:    comp.Month,
		Income:   centsToMoney(comp.IncomeCents, "CAD"),
		Expenses: centsToMoney(comp.ExpenseCents, "CAD"),
		Net:      centsToMoney(comp.NetCents, "CAD"),
	}
}

func topCategoryToPb(cat *sqlc.GetTopCategoriesRow) *pb.TopCategory {
	if cat == nil {
		return nil
	}

	return &pb.TopCategory{
		Slug:             cat.Slug,
		Color:            cat.Color,
		TransactionCount: cat.TransactionCount,
		TotalAmount:      centsToMoney(cat.TotalAmountCents, "CAD"),
	}
}

func topMerchantToPb(merchant *sqlc.GetTopMerchantsRow) *pb.TopMerchant {
	if merchant == nil {
		return nil
	}

	merchantName := ""
	if merchant.Merchant != nil {
		merchantName = *merchant.Merchant
	}

	return &pb.TopMerchant{
		Merchant:         merchantName,
		TransactionCount: merchant.TransactionCount,
		TotalAmount:      centsToMoney(merchant.TotalAmountCents, "CAD"),
		AvgAmount:        centsToMoney(merchant.AvgAmountCents, "CAD"),
	}
}

func accountBalanceToPb(row *sqlc.GetAccountBalancesRow) *pb.AccountBalance {
	if row == nil {
		return nil
	}

	return &pb.AccountBalance{
		Id:             row.ID,
		Name:           row.Name,
		AccountType:    pb.AccountType(row.AccountType),
		CurrentBalance: centsToMoney(row.BalanceCents, row.Currency),
		Currency:       row.Currency,
	}
}

func dashboardSummaryToPb(summary *sqlc.GetDashboardSummaryRow) *pb.DashboardSummary {
	if summary == nil {
		return nil
	}

	return &pb.DashboardSummary{
		TotalAccounts:             summary.TotalAccounts,
		TotalTransactions:         summary.TotalTransactions,
		TotalIncome:               centsToMoney(summary.TotalIncomeCents, "CAD"),
		TotalExpenses:             centsToMoney(summary.TotalExpenseCents, "CAD"),
		UncategorizedTransactions: summary.UncategorizedTransactions,
	}
}

func netWorthPointToPb(row *sqlc.GetNetWorthHistoryRow, currency string) *pb.NetWorthPoint {
	if row == nil {
		return nil
	}

	// parse the date string to time.Time for conversion
	pointDate, _ := time.Parse("2006-01-02", row.Date)
	return &pb.NetWorthPoint{
		Date:     timeToDate(pointDate),
		NetWorth: centsToMoney(row.NetWorthCents, currency),
	}
}

// ----- internal helpers --------------------------------------------------------------------

func startOfDay(t time.Time, loc *time.Location) time.Time {
	return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, loc)
}

func endOfDay(t time.Time, loc *time.Location) time.Time {
	return time.Date(t.Year(), t.Month(), t.Day(), 23, 59, 59, 999999999, loc)
}

func formatDateRange(start, end time.Time) string {
	if start.Year() == end.Year() && start.Month() == end.Month() {
		return fmt.Sprintf("%s %d-%d, %d",
			start.Month().String()[:3], start.Day(), end.Day(), start.Year())
	}
	return fmt.Sprintf("%s %d, %d - %s %d, %d",
		start.Month().String()[:3], start.Day(), start.Year(),
		end.Month().String()[:3], end.Day(), end.Year())
}

// TODO: this file is something....
