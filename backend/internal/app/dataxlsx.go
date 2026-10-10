package app

import (
	"fmt"
	"io"
	"strconv"
	"strings"

	"github.com/xuri/excelize/v2"
)

// The offline workbook: one tab per sheet plus a "Read me" tab. Each data tab
// ends with a "Delete" column (type yes to delete the row) and a hidden "_v"
// column holding the row's version stamp. Option lists live on a hidden
// "Lists" tab and drive the dropdowns.

const (
	readmeTab   = "Read me"
	listsTab    = "Lists"
	deleteHdr   = "Delete"
	versionHdr  = "_v"
	lockedHdr   = "Locked"
	maxDataRows = 5000 // dropdowns / formats reach this far below the data
)

func colName(i int) string { n, _ := excelize.ColumnNumberToName(i + 1); return n }

// WriteWorkbook writes every editable sheet as an .xlsx workbook.
func (a *App) WriteWorkbook(w io.Writer) error {
	specs, err := a.SheetSpecs()
	if err != nil {
		return err
	}
	x := excelize.NewFile()
	defer x.Close()
	x.SetSheetName("Sheet1", readmeTab)

	hdr, _ := x.NewStyle(&excelize.Style{Font: &excelize.Font{Bold: true, Color: "FFFFFF"}, Fill: excelize.Fill{Type: "pattern", Pattern: 1, Color: []string{"1F2937"}},
		Alignment: &excelize.Alignment{WrapText: true, Vertical: "center"}})
	hdrRO, _ := x.NewStyle(&excelize.Style{Font: &excelize.Font{Bold: true, Color: "FFFFFF"}, Fill: excelize.Fill{Type: "pattern", Pattern: 1, Color: []string{"9CA3AF"}},
		Alignment: &excelize.Alignment{WrapText: true, Vertical: "center"}})
	hdrDel, _ := x.NewStyle(&excelize.Style{Font: &excelize.Font{Bold: true, Color: "FFFFFF"}, Fill: excelize.Fill{Type: "pattern", Pattern: 1, Color: []string{"B91C1C"}},
		Alignment: &excelize.Alignment{Vertical: "center"}})
	textFmt := "@"
	text, _ := x.NewStyle(&excelize.Style{CustomNumFmt: &textFmt})
	textRO, _ := x.NewStyle(&excelize.Style{CustomNumFmt: &textFmt, Font: &excelize.Font{Color: "6B7280"}, Fill: excelize.Fill{Type: "pattern", Pattern: 1, Color: []string{"F3F4F6"}}})
	moneyFmt := "#,##0.00"
	money, _ := x.NewStyle(&excelize.Style{CustomNumFmt: &moneyFmt})
	moneyRO, _ := x.NewStyle(&excelize.Style{CustomNumFmt: &moneyFmt, Font: &excelize.Font{Color: "6B7280"}, Fill: excelize.Fill{Type: "pattern", Pattern: 1, Color: []string{"F3F4F6"}}})
	locked, _ := x.NewStyle(&excelize.Style{Font: &excelize.Font{Color: "B45309", Italic: true}})
	bold, _ := x.NewStyle(&excelize.Style{Font: &excelize.Font{Bold: true, Size: 13}})
	wrap, _ := x.NewStyle(&excelize.Style{Alignment: &excelize.Alignment{WrapText: true, Vertical: "top"}})

	// Read me
	readme := [][]string{
		{"Treasure Dash – bulk edit workbook"},
		{""},
		{"1. Edit the white columns. Grey columns are for reference and are ignored on upload."},
		{"2. Add a row at the bottom of a tab to create something new (leave ID blank)."},
		{"3. Type yes in the red Delete column to delete a row."},
		{"4. Upload the file on the Bulk edit page. You'll see every change before anything is saved."},
		{""},
		{"Only rows you changed are saved. If a row was also changed in the portal after you downloaded this file, it is flagged instead of overwritten – download again."},
		{"Rows marked Locked are managed elsewhere (bank-posted entries, automatic late fees). A database backup is taken before every save."},
		{"Dates: YYYY-MM-DD or DD/MM/YYYY. Amounts in rupees. Don't edit or delete the hidden _v column."},
		{""},
	}
	for _, s := range specs {
		readme = append(readme, []string{s.Title, s.Help})
	}
	for i, r := range readme {
		for j, v := range r {
			cell, _ := excelize.CoordinatesToCellName(j+1, i+1)
			x.SetCellStr(readmeTab, cell, v)
		}
	}
	x.SetCellStyle(readmeTab, "A1", "A1", bold)
	x.SetColWidth(readmeTab, "A", "A", 18)
	x.SetColWidth(readmeTab, "B", "B", 110)
	x.SetCellStyle(readmeTab, "B12", fmt.Sprintf("B%d", len(readme)), wrap)

	// option lists
	x.NewSheet(listsTab)
	listRef := map[string]string{}
	li := 0
	for _, s := range specs {
		for _, c := range s.Columns {
			opts := c.Options
			if c.Type == ColBool {
				opts = []string{"yes", "no"}
			}
			if len(opts) == 0 || c.ReadOnly {
				continue
			}
			key := s.Name + "." + c.Key
			col := colName(li)
			x.SetCellStr(listsTab, col+"1", key)
			for j, o := range opts {
				x.SetCellStr(listsTab, fmt.Sprintf("%s%d", col, j+2), o)
			}
			listRef[key] = fmt.Sprintf("'%s'!$%s$2:$%s$%d", listsTab, col, col, len(opts)+1)
			li++
		}
	}
	x.SetSheetVisible(listsTab, false)

	for _, s := range specs {
		data, err := a.Sheet(s.Name)
		if err != nil {
			return err
		}
		tab := s.Title
		x.NewSheet(tab)
		n := len(s.Columns)
		lockCol, delCol, verCol := colName(n), colName(n+1), colName(n+2)
		last := len(data.Rows) + 1
		for i, c := range s.Columns {
			cn := colName(i)
			x.SetCellStr(tab, cn+"1", c.Header)
			st := hdr
			if c.ReadOnly {
				st = hdrRO
			}
			x.SetCellStyle(tab, cn+"1", cn+"1", st)
			w := c.Width
			if w == 0 {
				w = 14
			}
			x.SetColWidth(tab, cn, cn, w)
			body := text
			if c.Type == ColMoney {
				body = money
			}
			if c.ReadOnly {
				body = textRO
				if c.Type == ColMoney {
					body = moneyRO
				}
				x.SetCellStyle(tab, cn+"2", fmt.Sprintf("%s%d", cn, last), body)
			} else {
				x.SetColStyle(tab, cn, body)
				x.SetCellStyle(tab, cn+"1", cn+"1", st)
			}
			if c.Help != "" {
				x.AddComment(tab, excelize.Comment{Cell: cn + "1", Author: "Treasure Dash", Paragraph: []excelize.RichTextRun{{Text: c.Help}}})
			}
			if ref, ok := listRef[s.Name+"."+c.Key]; ok {
				dv := excelize.NewDataValidation(true)
				dv.Sqref = fmt.Sprintf("%s2:%s%d", cn, cn, last+maxDataRows)
				dv.SetSqrefDropList(ref)
				x.AddDataValidation(tab, dv)
			}
		}
		x.SetCellStr(tab, lockCol+"1", lockedHdr)
		x.SetCellStyle(tab, lockCol+"1", lockCol+"1", hdrRO)
		x.SetColWidth(tab, lockCol, lockCol, 30)
		if s.CanDelete {
			x.SetCellStr(tab, delCol+"1", deleteHdr)
			x.SetCellStyle(tab, delCol+"1", delCol+"1", hdrDel)
			x.SetColWidth(tab, delCol, delCol, 8)
			dv := excelize.NewDataValidation(true)
			dv.Sqref = fmt.Sprintf("%s2:%s%d", delCol, delCol, last+maxDataRows)
			dv.SetDropList([]string{"yes"})
			x.AddDataValidation(tab, dv)
		}
		x.SetCellStr(tab, verCol+"1", versionHdr)
		x.SetColVisible(tab, verCol, false)

		for r, row := range data.Rows {
			rn := r + 2
			for i, c := range s.Columns {
				cell := fmt.Sprintf("%s%d", colName(i), rn)
				v := row.Values[c.Key]
				if v == "" {
					continue
				}
				if c.Type == ColMoney {
					if f, err := strconv.ParseFloat(v, 64); err == nil {
						x.SetCellFloat(tab, cell, f, 2, 64)
						continue
					}
				}
				x.SetCellStr(tab, cell, v)
			}
			if row.Locked != "" {
				cell := fmt.Sprintf("%s%d", lockCol, rn)
				x.SetCellStr(tab, cell, row.Locked)
				x.SetCellStyle(tab, cell, cell, locked)
			}
			x.SetCellStr(tab, fmt.Sprintf("%s%d", verCol, rn), row.Version)
		}
		x.SetPanes(tab, &excelize.Panes{Freeze: true, YSplit: 1, TopLeftCell: "A2", ActivePane: "bottomLeft"})
		x.AutoFilter(tab, fmt.Sprintf("A1:%s%d", lockCol, last), nil)
		x.SetRowHeight(tab, 1, 30)
	}
	x.SetActiveSheet(0)
	return x.Write(w)
}

// ReadWorkbook turns an uploaded workbook back into a changeset. Tabs are
// recognised by name and columns by their header, so columns may be
// reordered and unknown tabs/columns are ignored.
func (a *App) ReadWorkbook(r io.Reader) (Changeset, error) {
	specs, err := a.SheetSpecs()
	if err != nil {
		return nil, err
	}
	x, err := excelize.OpenReader(r)
	if err != nil {
		return nil, fmt.Errorf("not a readable .xlsx file: %w", err)
	}
	defer x.Close()
	cs := Changeset{}
	found := 0
	for _, tab := range x.GetSheetList() {
		var spec SheetSpec
		ok := false
		for _, s := range specs {
			if strings.EqualFold(strings.TrimSpace(tab), s.Title) {
				spec, ok = s, true
			}
		}
		if !ok {
			continue
		}
		found++
		rows, err := x.GetRows(tab, excelize.Options{RawCellValue: true})
		if err != nil {
			return nil, fmt.Errorf("tab %q: %w", tab, err)
		}
		if len(rows) == 0 {
			continue
		}
		keys := make([]string, len(rows[0]))
		for i, h := range rows[0] {
			h = strings.TrimSpace(h)
			switch {
			case strings.EqualFold(h, deleteHdr):
				keys[i] = "!delete"
			case h == versionHdr:
				keys[i] = "!v"
			default:
				for _, c := range spec.Columns {
					if strings.EqualFold(h, c.Header) {
						keys[i] = c.Key
					}
				}
			}
		}
		if !contains(keys, spec.Key) {
			return nil, fmt.Errorf("tab %q has no %q column", tab, spec.Columns[0].Header)
		}
		var out []InRow
		for ri, row := range rows[1:] {
			in := InRow{Row: ri + 2, Values: map[string]string{}}
			for i, v := range row {
				if i >= len(keys) || keys[i] == "" {
					continue
				}
				switch keys[i] {
				case "!delete":
					d, _ := normalize(Column{Type: ColBool}, v)
					in.Delete = d == "yes"
				case "!v":
					in.Version = strings.TrimSpace(v)
				default:
					in.Values[keys[i]] = v
				}
			}
			// a header column that's absent from this row (short row) is blank, not "unchanged"
			for _, k := range keys {
				if k != "" && k[0] != '!' {
					if _, ok := in.Values[k]; !ok {
						in.Values[k] = ""
					}
				}
			}
			if !in.Delete && allBlank(in.Values) {
				continue
			}
			out = append(out, in)
		}
		cs[spec.Name] = out
	}
	if found == 0 {
		return nil, fmt.Errorf("no recognised tabs – upload the workbook downloaded from this page")
	}
	return cs, nil
}

func contains(ss []string, s string) bool {
	for _, x := range ss {
		if x == s {
			return true
		}
	}
	return false
}
