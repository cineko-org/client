package cgv

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/cineko-org/client/internal/domain"
)

const seatDataPath = "/api/v1/booking/searchIfSeatData"

type seatNetworkResponse struct {
	body []byte
	err  error
}

type seatDataEnvelope struct {
	StatusCode int    `json:"statusCode"`
	ResultMsg  string `json:"resultMsg"`
	Data       struct {
		Items []seatDataItem `json:"items"`
	} `json:"data"`
}

type seatDataItem struct {
	Board     seatDataBoard      `json:"sbord"`
	SaleForms []seatDataSaleForm `json:"salfrms"`
	Zones     []seatDataZone     `json:"szones"`
	Blocks    []seatDataBlock    `json:"sblcks"`
	Seats     []seatDataSeat     `json:"seats"`
}

type seatDataBoard struct {
	XStart string `json:"xcoordStartVal"`
	YStart string `json:"ycoordStartVal"`
	XEnd   string `json:"xcoordEndVal"`
	YEnd   string `json:"ycoordEndVal"`
	Count  int    `json:"stcnt"`
}

type seatDataSaleForm struct {
	Code string `json:"seatSalfrmCd"`
	Name string `json:"seatSalfrmNm"`
}

type seatDataZone struct {
	Code     string `json:"szoneNo"`
	Name     string `json:"szoneNm"`
	KindCode string `json:"szoneKindCd"`
	KindName string `json:"szoneKindNm"`
	XStart   string `json:"xcoordStartVal"`
	YStart   string `json:"ycoordStartVal"`
	XEnd     string `json:"xcoordEndVal"`
	YEnd     string `json:"ycoordEndVal"`
	Capacity string `json:"maxNopsn"`
}

type seatDataBlock struct {
	Code     string `json:"sblckNo"`
	Name     string `json:"sblckNm"`
	KindCode string `json:"sblckKindCd"`
	KindName string `json:"sblckKindNm"`
	XStart   string `json:"xcoordStartVal"`
	YStart   string `json:"ycoordStartVal"`
	XEnd     string `json:"xcoordEndVal"`
	YEnd     string `json:"ycoordEndVal"`
}

type seatDataSeat struct {
	LocationID string `json:"seatLocNo"`
	Row        string `json:"seatRowNm"`
	Number     string `json:"seatNo"`
	KindCode   string `json:"stkndCd"`
	KindName   string `json:"stkndNm"`
	ZoneName   string `json:"szoneNm"`
	ZoneKind   string `json:"szoneKindNm"`
	SaleForm   string `json:"seatSalfrmCd"`
	StatusCode string `json:"seatStusCd"`
	StatusName string `json:"seatStusNm"`
	SaleYN     string `json:"seatSaleYn"`
	XStart     string `json:"xcoordStartVal"`
	YStart     string `json:"ycoordStartVal"`
	XEnd       string `json:"xcoordEndVal"`
	YEnd       string `json:"ycoordEndVal"`
	LeftAisle  string `json:"leftPwayYn"`
	RightAisle string `json:"rghtPwayYn"`
}

type parsedSeatSnapshot struct {
	Seats    []domain.Seat
	Zones    []domain.LayoutZone
	Blocks   []domain.LayoutBlock
	Live     []domain.LiveSeat
	Hash     string
	Captured time.Time
}

func parseSeatSnapshot(body []byte, auditoriumID string, now time.Time) (parsedSeatSnapshot, error) {
	var envelope seatDataEnvelope
	if err := json.Unmarshal(body, &envelope); err != nil {
		return parsedSeatSnapshot{}, fmt.Errorf("decode CGV seat snapshot: %w", err)
	}
	if envelope.StatusCode != 0 {
		return parsedSeatSnapshot{}, fmt.Errorf("CGV seat snapshot failed: %s", envelope.ResultMsg)
	}
	if len(envelope.Data.Items) == 0 {
		return parsedSeatSnapshot{}, errors.New("CGV seat snapshot contained no layout")
	}
	snapshot := parsedSeatSnapshot{
		Hash: snapshotHash(body), Captured: now,
	}
	labels := make(map[string]struct{})
	for _, item := range envelope.Data.Items {
		if err := appendSeatDataItem(&snapshot, item, auditoriumID, now, labels); err != nil {
			return parsedSeatSnapshot{}, err
		}
	}
	return snapshot, nil
}

func appendSeatDataItem(
	snapshot *parsedSeatSnapshot,
	item seatDataItem,
	auditoriumID string,
	now time.Time,
	labels map[string]struct{},
) error {
	board, err := newCoordinateBoard(item.Board)
	if err != nil {
		return err
	}
	saleForms := make(map[string]string, len(item.SaleForms))
	for _, saleForm := range item.SaleForms {
		saleForms[saleForm.Code] = saleForm.Name
	}
	itemSeatStart := len(snapshot.Seats)
	for _, source := range item.Seats {
		number, parseErr := strconv.Atoi(source.Number)
		if parseErr != nil {
			return fmt.Errorf("parse seat %s%s number: %w", source.Row, source.Number, parseErr)
		}
		label := strings.ToUpper(strings.TrimSpace(source.Row)) + strconv.Itoa(number)
		if _, duplicate := labels[label]; duplicate {
			return fmt.Errorf("duplicate seat label across CGV seat areas: %s", label)
		}
		labels[label] = struct{}{}
		saleFormName := saleForms[source.SaleForm]
		features := seatFeatures(source, saleFormName)
		snapshot.Seats = append(snapshot.Seats, domain.Seat{
			AuditoriumID: auditoriumID, Label: label, Row: strings.ToUpper(strings.TrimSpace(source.Row)),
			Number: number, X: board.centerX(source.XStart, source.XEnd),
			Y: board.centerY(source.YStart, source.YEnd), Type: sourceSeatType(source, saleFormName),
			ZoneName: source.ZoneName, ZoneKind: source.ZoneKind,
			SaleFormCode: source.SaleForm, SaleFormName: saleFormName,
			LeftAisle: source.LeftAisle == "Y", RightAisle: source.RightAisle == "Y",
			Features: features, SourceLabel: label,
			SourceSeatKindCode: source.KindCode, SourceSeatKindName: source.KindName,
		})
		snapshot.Live = append(snapshot.Live, domain.LiveSeat{
			Label: label, Available: source.SaleYN == "Y" && source.StatusCode == "00",
			StatusCode: source.StatusCode, StatusName: source.StatusName,
			SaleFormCode: source.SaleForm, ObservedAt: now, Source: "cgv-seat-snapshot",
		})
	}
	for _, source := range item.Zones {
		capacity, _ := strconv.Atoi(source.Capacity)
		snapshot.Zones = append(snapshot.Zones, domain.LayoutZone{
			Code: source.Code, Name: source.Name, KindCode: source.KindCode, KindName: source.KindName,
			MinX: board.x(source.XStart), MaxX: board.x(source.XEnd),
			MinY: board.y(source.YStart), MaxY: board.y(source.YEnd), Capacity: capacity,
		})
	}
	for _, source := range item.Blocks {
		snapshot.Blocks = append(snapshot.Blocks, domain.LayoutBlock{
			Code: source.Code, Name: source.Name, KindCode: source.KindCode, KindName: source.KindName,
			MinX: board.x(source.XStart), MaxX: board.x(source.XEnd),
			MinY: board.y(source.YStart), MaxY: board.y(source.YEnd),
		})
	}
	itemSeatCount := len(snapshot.Seats) - itemSeatStart
	if item.Board.Count > 0 && item.Board.Count != itemSeatCount {
		return fmt.Errorf("CGV board count %d differs from %d seats", item.Board.Count, itemSeatCount)
	}
	return nil
}

type coordinateBoard struct {
	minX float64
	maxX float64
	minY float64
	maxY float64
}

func newCoordinateBoard(source seatDataBoard) (coordinateBoard, error) {
	board := coordinateBoard{
		minX: coordinate(source.XStart), maxX: coordinate(source.XEnd),
		minY: coordinate(source.YStart), maxY: coordinate(source.YEnd),
	}
	if board.maxX <= board.minX || board.maxY <= board.minY {
		return coordinateBoard{}, errors.New("CGV seat snapshot has invalid board bounds")
	}
	return board, nil
}

func (board coordinateBoard) x(value string) float64 {
	return normalizeAxis(coordinate(value), board.minX, board.maxX)
}

func (board coordinateBoard) y(value string) float64 {
	return normalizeAxis(coordinate(value), board.minY, board.maxY)
}

func (board coordinateBoard) centerX(start, end string) float64 {
	return normalizeAxis((coordinate(start)+coordinate(end))/2, board.minX, board.maxX)
}

func (board coordinateBoard) centerY(start, end string) float64 {
	return normalizeAxis((coordinate(start)+coordinate(end))/2, board.minY, board.maxY)
}

func coordinate(value string) float64 {
	parsed, _ := strconv.ParseFloat(value, 64)
	return parsed
}

func sourceSeatType(source seatDataSeat, saleFormName string) domain.SeatType {
	if strings.Contains(saleFormName, "이동식") {
		return domain.SeatTypeWheelchair
	}
	return inferSeatType(source.KindName+" "+saleFormName, nil)
}

func seatFeatures(source seatDataSeat, saleFormName string) []string {
	features := []string{"zone:" + source.ZoneName, "sale-form:" + saleFormName}
	if source.LeftAisle == "Y" {
		features = append(features, "left-aisle")
	}
	if source.RightAisle == "Y" {
		features = append(features, "right-aisle")
	}
	if strings.Contains(saleFormName, "이동식") {
		features = append(features, "wheelchair-area", "removable")
	}
	return features
}

func snapshotHash(body []byte) string {
	hash := sha256.Sum256(body)
	return hex.EncodeToString(hash[:])
}
