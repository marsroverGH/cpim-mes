package service

import (
	"context"
	"encoding/csv"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"

	"github.com/cpim-mes/backend/internal/domain"
	"github.com/cpim-mes/backend/internal/repository"
)

// CSVService — Items の CSV インポート/エクスポート
type CSVService struct {
	repos *repository.Repositories
}

func NewCSVService(r *repository.Repositories) *CSVService {
	return &CSVService{repos: r}
}

// CSV列順 (ヘッダー必須):
// code,name,type,uom,leadTimeDays,safetyStock,lotSize,standardCost
var itemCSVHeader = []string{
	"code", "name", "type", "uom",
	"leadTimeDays", "safetyStock", "lotSize", "standardCost",
}

func (s *CSVService) ExportItems(ctx context.Context, w io.Writer) error {
	items, err := s.repos.Items.List(ctx)
	if err != nil {
		return err
	}
	cw := csv.NewWriter(w)
	if err := cw.Write(itemCSVHeader); err != nil {
		return err
	}
	for _, it := range items {
		row := []string{
			it.Code, it.Name, string(it.Type), it.UoM,
			strconv.Itoa(it.LeadTimeDays),
			strconv.FormatFloat(it.SafetyStock, 'f', -1, 64),
			strconv.FormatFloat(it.LotSize, 'f', -1, 64),
			strconv.FormatFloat(it.StandardCost, 'f', -1, 64),
		}
		if err := cw.Write(row); err != nil {
			return err
		}
	}
	cw.Flush()
	return cw.Error()
}

type ImportResult struct {
	Inserted int      `json:"inserted"`
	Updated  int      `json:"updated"`
	Skipped  int      `json:"skipped"`
	Errors   []string `json:"errors"`
}

// ImportItems — CSV を読み、code 一意キーで Upsert する
func (s *CSVService) ImportItems(ctx context.Context, r io.Reader) (*ImportResult, error) {
	cr := csv.NewReader(r)
	cr.FieldsPerRecord = -1 // tolerate variable

	header, err := cr.Read()
	if err != nil {
		return nil, fmt.Errorf("read header: %w", err)
	}
	headerMap := make(map[string]int)
	for i, h := range header {
		headerMap[strings.TrimSpace(h)] = i
	}
	for _, h := range itemCSVHeader {
		if _, ok := headerMap[h]; !ok {
			return nil, fmt.Errorf("missing column %q (expected: %s)", h, strings.Join(itemCSVHeader, ","))
		}
	}

	// 既存 items を code で索引化
	existing, err := s.repos.Items.List(ctx)
	if err != nil {
		return nil, err
	}
	byCode := make(map[string]domain.Item, len(existing))
	for _, e := range existing {
		byCode[e.Code] = e
	}

	res := &ImportResult{}
	rowNum := 1 // header was row 1
	for {
		rowNum++
		rec, err := cr.Read()
		if err == io.EOF {
			break
		}
		if err != nil {
			res.Errors = append(res.Errors, fmt.Sprintf("row %d: %v", rowNum, err))
			res.Skipped++
			continue
		}

		it, perr := parseItemRow(rec, headerMap)
		if perr != nil {
			res.Errors = append(res.Errors, fmt.Sprintf("row %d: %v", rowNum, perr))
			res.Skipped++
			continue
		}

		if cur, ok := byCode[it.Code]; ok {
			it.ID = cur.ID
			if err := s.repos.Items.Update(ctx, &it); err != nil {
				res.Errors = append(res.Errors, fmt.Sprintf("row %d update: %v", rowNum, err))
				res.Skipped++
				continue
			}
			res.Updated++
		} else {
			if err := s.repos.Items.Create(ctx, &it); err != nil {
				res.Errors = append(res.Errors, fmt.Sprintf("row %d insert: %v", rowNum, err))
				res.Skipped++
				continue
			}
			res.Inserted++
		}
	}
	return res, nil
}

func parseItemRow(rec []string, h map[string]int) (domain.Item, error) {
	get := func(key string) string {
		i, ok := h[key]
		if !ok || i >= len(rec) {
			return ""
		}
		return strings.TrimSpace(rec[i])
	}
	parseFloat := func(s string, def float64) (float64, error) {
		if s == "" {
			return def, nil
		}
		return strconv.ParseFloat(s, 64)
	}

	code := get("code")
	if code == "" {
		return domain.Item{}, errors.New("code is required")
	}
	name := get("name")
	if name == "" {
		return domain.Item{}, errors.New("name is required")
	}
	tp := domain.ItemType(strings.ToUpper(get("type")))
	switch tp {
	case domain.ItemTypeFinished, domain.ItemTypeSubAssembly,
		domain.ItemTypeRawMaterial, domain.ItemTypePurchasedPart:
	default:
		return domain.Item{}, fmt.Errorf("invalid type %q (must be FG/SA/RM/PP)", tp)
	}
	uom := get("uom")
	if uom == "" {
		uom = "EA"
	}

	ltStr := get("leadTimeDays")
	lt := 0
	if ltStr != "" {
		v, err := strconv.Atoi(ltStr)
		if err != nil {
			return domain.Item{}, fmt.Errorf("leadTimeDays: %w", err)
		}
		lt = v
	}
	ss, err := parseFloat(get("safetyStock"), 0)
	if err != nil {
		return domain.Item{}, fmt.Errorf("safetyStock: %w", err)
	}
	ls, err := parseFloat(get("lotSize"), 1)
	if err != nil {
		return domain.Item{}, fmt.Errorf("lotSize: %w", err)
	}
	sc, err := parseFloat(get("standardCost"), 0)
	if err != nil {
		return domain.Item{}, fmt.Errorf("standardCost: %w", err)
	}

	return domain.Item{
		Code: code, Name: name, Type: tp, UoM: uom,
		LeadTimeDays: lt, SafetyStock: ss, LotSize: ls, StandardCost: sc,
	}, nil
}
