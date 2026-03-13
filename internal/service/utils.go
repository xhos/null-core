package service

import (
	"errors"
	"fmt"
	"math"
	"time"

	pb "null-core/internal/gen/null/v1"

	"google.golang.org/genproto/googleapis/type/date"
	"google.golang.org/protobuf/types/known/timestamppb"
)

var (
	ErrValidation    = errors.New("validation failed")
	ErrUnimplemented = errors.New("unimplemented")
)

func wrapErr(op string, err error) error {
	knownErrors := []error{
		ErrValidation,
		ErrUnimplemented,
	}

	for _, knownErr := range knownErrors {
		if errors.Is(err, knownErr) {
			return fmt.Errorf("%s: %w", op, knownErr)
		}
	}

	return fmt.Errorf("%s: %w", op, err)
}

func int32Ptr(i int32) *int32 {
	return &i
}

func toProtoTimestamp(t *time.Time) *timestamppb.Timestamp {
	if t == nil || t.IsZero() {
		return nil
	}
	return timestamppb.New(*t)
}

func fromProtoTimestamp(ts *timestamppb.Timestamp) time.Time {
	if ts == nil || !ts.IsValid() {
		return time.Time{}
	}
	return ts.AsTime()
}

func moneyToCents(m *pb.Money) int64 {
	if m == nil {
		return 0
	}
	return int64(math.Round(m.Amount * 100))
}

func centsToMoney(cents int64, currency string) *pb.Money {
	return &pb.Money{
		Amount:       float64(cents) / 100,
		CurrencyCode: currency,
	}
}

func dateToTime(d *date.Date) *time.Time {
	if d == nil {
		return nil
	}
	t := time.Date(int(d.Year), time.Month(d.Month), int(d.Day), 0, 0, 0, 0, time.UTC)
	return &t
}

func timeToDate(t time.Time) *date.Date {
	if t.IsZero() {
		return nil
	}
	return &date.Date{
		Year:  int32(t.Year()),
		Month: int32(t.Month()),
		Day:   int32(t.Day()),
	}
}
