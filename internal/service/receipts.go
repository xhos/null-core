package service

import (
	"bytes"
	"context"
	"crypto/tls"
	"fmt"
	"image"
	"image/jpeg"
	"image/png"
	"math"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"null-core/internal/db/sqlc"
	pb "null-core/internal/gen/null/v1"
	"null-core/internal/gen/null/v1/nullv1connect"

	"connectrpc.com/connect"
	"github.com/charmbracelet/log"
	"github.com/google/uuid"
	"golang.org/x/image/draw"
	_ "golang.org/x/image/webp"
	"golang.org/x/net/http2"
	"google.golang.org/genproto/googleapis/type/money"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// ----- interface ---------------------------------------------------------------------------

type ReceiptService interface {
	Upload(ctx context.Context, userID uuid.UUID, imageData []byte, contentType string) (*pb.Receipt, error)
	Get(ctx context.Context, userID uuid.UUID, id int64) (*pb.Receipt, []*pb.ReceiptLinkCandidate, error)
	List(ctx context.Context, userID uuid.UUID, req *pb.ListReceiptsRequest) ([]*pb.Receipt, int64, error)
	Update(ctx context.Context, userID uuid.UUID, id int64, req *pb.UpdateReceiptRequest) (*pb.Receipt, error)
	Delete(ctx context.Context, userID uuid.UUID, id int64) error
	StartWorker(ctx context.Context)
}

type rcptSvc struct {
	queries   *sqlc.Queries
	log       *log.Logger
	ocrClient nullv1connect.ReceiptOCRServiceClient
	dataDir   string
}

func newRcptSvc(queries *sqlc.Queries, logger *log.Logger, ocrURL string, dataDir string) ReceiptService {
	var ocrClient nullv1connect.ReceiptOCRServiceClient
	if ocrURL != "" {
		// gRPC requires HTTP/2; over plaintext that means h2c,
		// which http.DefaultClient doesn't support.
		h2cClient := &http.Client{
			Transport: &http2.Transport{
				AllowHTTP: true,
				DialTLSContext: func(ctx context.Context, network, addr string, _ *tls.Config) (net.Conn, error) {
					var d net.Dialer
					return d.DialContext(ctx, network, addr)
				},
			},
		}
		ocrClient = nullv1connect.NewReceiptOCRServiceClient(
			h2cClient,
			ocrURL,
			connect.WithGRPC(),
		)
	}
	return &rcptSvc{
		queries:   queries,
		log:       logger,
		ocrClient: ocrClient,
		dataDir:   dataDir,
	}
}

// ----- content type helpers ----------------------------------------------------------------

var contentTypeToExt = map[string]string{
	"image/jpeg": "jpg",
	"image/png":  "png",
	"image/webp": "webp",
	"image/heic": "heic",
}

func extToContentType(ext string) string {
	switch ext {
	case ".jpg", ".jpeg":
		return "image/jpeg"
	case ".png":
		return "image/png"
	case ".webp":
		return "image/webp"
	case ".heic":
		return "image/heic"
	default:
		return "application/octet-stream"
	}
}

// ----- image resize ------------------------------------------------------------------------

const maxImageDim = 1920

// resizeImage downscales an image so its longest side is at most maxImageDim.
// Supported inputs: JPEG, PNG, WebP (via registered decoder).
// Output is always JPEG. Returns original bytes unchanged for HEIC or on error.
func resizeImage(data []byte, contentType string) ([]byte, string) {
	// HEIC has no pure-Go decoder — pass through
	if contentType == "image/heic" {
		return data, contentType
	}

	img, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return data, contentType
	}

	bounds := img.Bounds()
	w, h := bounds.Dx(), bounds.Dy()

	longest := w
	if h > longest {
		longest = h
	}
	if longest <= maxImageDim {
		return data, contentType
	}

	scale := float64(maxImageDim) / float64(longest)
	newW := int(math.Round(float64(w) * scale))
	newH := int(math.Round(float64(h) * scale))

	dst := image.NewRGBA(image.Rect(0, 0, newW, newH))
	draw.BiLinear.Scale(dst, dst.Bounds(), img, bounds, draw.Over, nil)

	var buf bytes.Buffer
	if contentType == "image/png" {
		if err := png.Encode(&buf, dst); err != nil {
			return data, contentType
		}
		return buf.Bytes(), "image/png"
	}

	// JPEG for everything else (jpeg, webp input)
	if err := jpeg.Encode(&buf, dst, &jpeg.Options{Quality: 85}); err != nil {
		return data, contentType
	}
	return buf.Bytes(), "image/jpeg"
}

// ----- methods -----------------------------------------------------------------------------

func (s *rcptSvc) Upload(ctx context.Context, userID uuid.UUID, imageData []byte, contentType string) (*pb.Receipt, error) {
	ext, ok := contentTypeToExt[contentType]
	if !ok {
		return nil, fmt.Errorf("ReceiptService.Upload: unsupported content type %q: %w", contentType, ErrValidation)
	}

	imageData, contentType = resizeImage(imageData, contentType)
	ext = contentTypeToExt[contentType]

	relPath := filepath.Join("receipts", userID.String(), uuid.New().String()+"."+ext)
	absPath := filepath.Join(s.dataDir, relPath)

	if err := os.MkdirAll(filepath.Dir(absPath), 0755); err != nil {
		return nil, fmt.Errorf("ReceiptService.Upload: mkdir: %w", err)
	}
	if err := os.WriteFile(absPath, imageData, 0644); err != nil {
		return nil, fmt.Errorf("ReceiptService.Upload: write file: %w", err)
	}

	row, err := s.queries.CreateReceipt(ctx, sqlc.CreateReceiptParams{
		UserID:    userID,
		ImagePath: relPath,
		Status:    int16(pb.ReceiptStatus_RECEIPT_STATUS_PENDING),
	})
	if err != nil {
		// clean up file on DB error
		os.Remove(absPath)
		return nil, wrapErr("ReceiptService.Upload", err)
	}

	return s.receiptToPb(&row, nil), nil
}

func (s *rcptSvc) Get(ctx context.Context, userID uuid.UUID, id int64) (*pb.Receipt, []*pb.ReceiptLinkCandidate, error) {
	row, err := s.queries.GetReceipt(ctx, sqlc.GetReceiptParams{
		ID:     id,
		UserID: userID,
	})
	if err != nil {
		return nil, nil, wrapErr("ReceiptService.Get", err)
	}

	items, err := s.queries.ListReceiptItems(ctx, row.ID)
	if err != nil {
		return nil, nil, wrapErr("ReceiptService.Get.Items", err)
	}

	receipt := s.receiptToPb(&row, items)

	// link_candidates is out of scope — return empty
	return receipt, nil, nil
}

func (s *rcptSvc) List(ctx context.Context, userID uuid.UUID, req *pb.ListReceiptsRequest) ([]*pb.Receipt, int64, error) {
	params := sqlc.ListReceiptsParams{
		UserID: userID,
	}

	if req.Limit != nil {
		params.Lim = req.Limit
	}
	if req.Offset != nil {
		params.Off = req.Offset
	}
	if req.Status != nil {
		st := int16(*req.Status)
		params.Status = &st
	}
	if req.UnlinkedOnly != nil {
		params.UnlinkedOnly = req.UnlinkedOnly
	}
	if req.StartDate != nil {
		t := dateToTime(req.StartDate)
		params.StartDate = t
	}
	if req.EndDate != nil {
		t := dateToTime(req.EndDate)
		params.EndDate = t
	}

	rows, err := s.queries.ListReceipts(ctx, params)
	if err != nil {
		return nil, 0, wrapErr("ReceiptService.List", err)
	}

	var totalCount int64
	receipts := make([]*pb.Receipt, len(rows))
	for i := range rows {
		if i == 0 {
			totalCount = rows[i].TotalCount
		}

		items, err := s.queries.ListReceiptItems(ctx, rows[i].ID)
		if err != nil {
			return nil, 0, wrapErr("ReceiptService.List.Items", err)
		}

		receipts[i] = s.listRowToPb(&rows[i], items)
	}

	return receipts, totalCount, nil
}

func (s *rcptSvc) Update(ctx context.Context, userID uuid.UUID, id int64, req *pb.UpdateReceiptRequest) (*pb.Receipt, error) {
	params := sqlc.UpdateReceiptParams{
		ID:     id,
		UserID: userID,
	}

	if req.TransactionId != nil {
		params.TransactionID = req.TransactionId
	}

	row, err := s.queries.UpdateReceipt(ctx, params)
	if err != nil {
		return nil, wrapErr("ReceiptService.Update", err)
	}

	// if items provided, replace all items
	if len(req.Items) > 0 {
		if err := s.queries.DeleteReceiptItemsByReceipt(ctx, row.ID); err != nil {
			return nil, wrapErr("ReceiptService.Update.DeleteItems", err)
		}
		for i, item := range req.Items {
			currency := "CAD"
			if row.Currency != nil {
				currency = *row.Currency
			}
			_, err := s.queries.CreateReceiptItem(ctx, sqlc.CreateReceiptItemParams{
				ReceiptID:      row.ID,
				RawName:        item.RawName,
				Name:           item.Name,
				Quantity:       item.Quantity,
				UnitPriceCents: item.UnitPriceCents,
				UnitCurrency:   currency,
				SortOrder:      int32(i),
			})
			if err != nil {
				return nil, wrapErr("ReceiptService.Update.CreateItem", err)
			}
		}
	}

	items, err := s.queries.ListReceiptItems(ctx, row.ID)
	if err != nil {
		return nil, wrapErr("ReceiptService.Update.ListItems", err)
	}

	return s.receiptToPb(&row, items), nil
}

func (s *rcptSvc) Delete(ctx context.Context, userID uuid.UUID, id int64) error {
	row, err := s.queries.GetReceipt(ctx, sqlc.GetReceiptParams{
		ID:     id,
		UserID: userID,
	})
	if err != nil {
		return wrapErr("ReceiptService.Delete", err)
	}

	if err := s.queries.DeleteReceipt(ctx, sqlc.DeleteReceiptParams{
		ID:     id,
		UserID: userID,
	}); err != nil {
		return wrapErr("ReceiptService.Delete", err)
	}

	absPath := filepath.Join(s.dataDir, row.ImagePath)
	if err := os.Remove(absPath); err != nil {
		s.log.Warn("failed to remove receipt image", "path", absPath, "error", err)
	}

	return nil
}

// ----- background worker -------------------------------------------------------------------

func (s *rcptSvc) StartWorker(ctx context.Context) {
	if s.ocrClient == nil {
		s.log.Warn("OCR client not configured, receipt worker disabled")
		return
	}

	s.log.Info("receipt OCR worker started")

	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			s.log.Info("receipt OCR worker stopped")
			return
		case <-ticker.C:
			s.processPendingReceipts(ctx)
		}
	}
}

func (s *rcptSvc) processPendingReceipts(ctx context.Context) {
	pending, err := s.queries.GetPendingReceipts(ctx)
	if err != nil {
		s.log.Error("failed to query pending receipts", "error", err)
		return
	}

	for _, receipt := range pending {
		s.processOneReceipt(ctx, receipt)
	}
}

func (s *rcptSvc) processOneReceipt(ctx context.Context, receipt sqlc.Receipt) {
	absPath := filepath.Join(s.dataDir, receipt.ImagePath)
	imageData, err := os.ReadFile(absPath)
	if err != nil {
		s.log.Error("failed to read receipt image", "id", receipt.ID, "path", absPath, "error", err)
		s.setReceiptFailed(ctx, receipt)
		return
	}

	contentType := extToContentType(filepath.Ext(receipt.ImagePath))

	ocrCtx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()

	resp, err := s.ocrClient.ParseReceipt(ocrCtx, connect.NewRequest(&pb.ParseReceiptRequest{
		ImageData:   imageData,
		ContentType: contentType,
	}))
	if err != nil {
		s.log.Error("OCR request failed", "id", receipt.ID, "error", err)
		s.setReceiptFailed(ctx, receipt)
		return
	}

	if !resp.Msg.Success || resp.Msg.Data == nil {
		errMsg := "unknown"
		if resp.Msg.Error != nil {
			errMsg = resp.Msg.Error.Message
		}
		s.log.Error("OCR parsing failed", "id", receipt.ID, "error", errMsg)
		s.setReceiptFailed(ctx, receipt)
		return
	}

	parsed := resp.Msg.Data

	updateParams := sqlc.UpdateReceiptParams{
		ID:     receipt.ID,
		UserID: receipt.UserID,
	}

	parsedStatus := int16(pb.ReceiptStatus_RECEIPT_STATUS_PARSED)
	updateParams.Status = &parsedStatus

	if parsed.Merchant != nil {
		updateParams.Merchant = parsed.Merchant
	}
	if parsed.Currency != nil {
		updateParams.Currency = parsed.Currency
	}
	if parsed.Confidence > 0 {
		updateParams.Confidence = &parsed.Confidence
	}

	if parsed.Date != nil {
		t, parseErr := time.Parse("2006-01-02", *parsed.Date)
		if parseErr == nil {
			updateParams.ReceiptDate = &t
		}
	}

	currency := "CAD"
	if parsed.Currency != nil {
		currency = *parsed.Currency
	}

	if parsed.Subtotal != nil {
		cents := dollarsToCents(*parsed.Subtotal)
		updateParams.SubtotalCents = &cents
	}
	if parsed.Tax != nil {
		cents := dollarsToCents(*parsed.Tax)
		updateParams.TaxCents = &cents
	}
	if parsed.Total != nil {
		cents := dollarsToCents(*parsed.Total)
		updateParams.TotalCents = &cents
	}

	if _, err := s.queries.UpdateReceipt(ctx, updateParams); err != nil {
		s.log.Error("failed to update receipt from OCR", "id", receipt.ID, "error", err)
		return
	}

	// insert parsed items
	for i, item := range parsed.Items {
		_, err := s.queries.CreateReceiptItem(ctx, sqlc.CreateReceiptItemParams{
			ReceiptID:      receipt.ID,
			RawName:        item.Raw,
			Name:           item.Name,
			Quantity:       item.Qty,
			UnitPriceCents: dollarsToCents(item.UnitPrice),
			UnitCurrency:   currency,
			SortOrder:      int32(i),
		})
		if err != nil {
			s.log.Error("failed to create receipt item from OCR", "receipt_id", receipt.ID, "error", err)
		}
	}

	s.log.Info("receipt parsed successfully", "id", receipt.ID, "merchant", parsed.GetMerchant(), "items", len(parsed.Items))
}

func (s *rcptSvc) setReceiptFailed(ctx context.Context, receipt sqlc.Receipt) {
	failedStatus := int16(pb.ReceiptStatus_RECEIPT_STATUS_FAILED)
	_, err := s.queries.UpdateReceipt(ctx, sqlc.UpdateReceiptParams{
		ID:     receipt.ID,
		UserID: receipt.UserID,
		Status: &failedStatus,
	})
	if err != nil {
		s.log.Error("failed to set receipt status to FAILED", "id", receipt.ID, "error", err)
	}
}

// ----- conversion helpers ------------------------------------------------------------------

func dollarsToCents(dollars float64) int64 {
	return int64(math.Round(dollars * 100))
}

func (s *rcptSvc) receiptToPb(r *sqlc.Receipt, items []sqlc.ReceiptItem) *pb.Receipt {
	proto := &pb.Receipt{
		Id:            r.ID,
		UserId:        r.UserID.String(),
		TransactionId: r.TransactionID,
		ImagePath:     r.ImagePath,
		Merchant:      r.Merchant,
		Currency:      r.Currency,
		Confidence:    r.Confidence,
		Status:        pb.ReceiptStatus(r.Status),
		CreatedAt:     timestamppb.New(r.CreatedAt),
		UpdatedAt:     timestamppb.New(r.UpdatedAt),
	}

	if r.ReceiptDate != nil {
		proto.ReceiptDate = timeToDate(*r.ReceiptDate)
	}

	currency := "CAD"
	if r.Currency != nil {
		currency = *r.Currency
	}

	if r.SubtotalCents != nil {
		proto.Subtotal = centsToMoney(*r.SubtotalCents, currency)
	}
	if r.TaxCents != nil {
		proto.Tax = centsToMoney(*r.TaxCents, currency)
	}
	if r.TotalCents != nil {
		proto.Total = centsToMoney(*r.TotalCents, currency)
	}

	proto.Items = make([]*pb.ReceiptItem, len(items))
	for i := range items {
		proto.Items[i] = receiptItemToPb(&items[i])
	}

	return proto
}

func (s *rcptSvc) listRowToPb(r *sqlc.ListReceiptsRow, items []sqlc.ReceiptItem) *pb.Receipt {
	proto := &pb.Receipt{
		Id:            r.ID,
		UserId:        r.UserID.String(),
		TransactionId: r.TransactionID,
		ImagePath:     r.ImagePath,
		Merchant:      r.Merchant,
		Currency:      r.Currency,
		Confidence:    r.Confidence,
		Status:        pb.ReceiptStatus(r.Status),
		CreatedAt:     timestamppb.New(r.CreatedAt),
		UpdatedAt:     timestamppb.New(r.UpdatedAt),
	}

	if r.ReceiptDate != nil {
		proto.ReceiptDate = timeToDate(*r.ReceiptDate)
	}

	currency := "CAD"
	if r.Currency != nil {
		currency = *r.Currency
	}

	if r.SubtotalCents != nil {
		proto.Subtotal = centsToMoney(*r.SubtotalCents, currency)
	}
	if r.TaxCents != nil {
		proto.Tax = centsToMoney(*r.TaxCents, currency)
	}
	if r.TotalCents != nil {
		proto.Total = centsToMoney(*r.TotalCents, currency)
	}

	proto.Items = make([]*pb.ReceiptItem, len(items))
	for i := range items {
		proto.Items[i] = receiptItemToPb(&items[i])
	}

	return proto
}

func receiptItemToPb(item *sqlc.ReceiptItem) *pb.ReceiptItem {
	return &pb.ReceiptItem{
		Id:        item.ID,
		ReceiptId: item.ReceiptID,
		RawName:   item.RawName,
		Name:      item.Name,
		Quantity:  item.Quantity,
		UnitPrice: &money.Money{
			CurrencyCode: item.UnitCurrency,
			Units:        item.UnitPriceCents / 100,
			Nanos:        int32((item.UnitPriceCents % 100) * 10_000_000),
		},
		SortOrder: item.SortOrder,
	}
}
