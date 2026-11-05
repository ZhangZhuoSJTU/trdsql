package trdsql

import (
	"encoding/csv"
	"errors"
	"fmt"
	"io"
	"strconv"
)

// CSVReader provides methods of the Reader interface.
type CSVReader struct {
	reader    *csv.Reader
	inNULL    string
	names     []string
	types     []string
	preRead   [][]string
	limitRead bool
	needNULL  bool
	uniform   bool
}

// wideTable is the number of columns at which a table counts as wide
// enough for the uniform fast path to pay off.
const wideTable = 8

// uniformLayout reports whether the pre-read rows establish a fixed
// column layout: every row already carries the full width, so records
// from the same producer can be copied into place without reconciling
// each field against the column count.
func uniformLayout(names []string, preRead [][]string) bool {
	if len(names) < wideTable {
		return false
	}
	for _, row := range preRead {
		if len(row) != len(names) {
			return false
		}
	}
	return true
}

// NewCSVReader returns CSVReader and error.
func NewCSVReader(reader io.Reader, opts *ReadOpts) (*CSVReader, error) {
	r := &CSVReader{}
	r.reader = csv.NewReader(reader)
	r.reader.LazyQuotes = true
	r.reader.FieldsPerRecord = -1 // no check count

	d, err := delimiter(opts.InDelimiter)
	if err != nil {
		return nil, err
	}
	r.reader.Comma = d

	if r.reader.Comma == ' ' {
		r.reader.TrimLeadingSpace = true
	}

	if opts.InSkip > 0 {
		skipRead(r, opts.InSkip)
	}

	r.needNULL = opts.InNeedNULL
	r.inNULL = opts.InNULL

	r.limitRead = opts.InLimitRead

	// Read the header.
	preReadN := opts.InPreRead
	if opts.InHeader {
		row, err := r.reader.Read()
		if err != nil {
			if !errors.Is(err, io.EOF) {
				return nil, err
			}
		}
		r.names = make([]string, len(row))
		for i, col := range row {
			r.names[i] = col
			if col == "" {
				r.names[i] = "c" + strconv.Itoa(i+1)
			}
		}
		preReadN--
	}

	// Pre-read and stored in slices.
	for n := 0; n < preReadN; n++ {
		row, err := r.reader.Read()
		if err != nil {
			if !errors.Is(err, io.EOF) {
				return r, err
			}
			r.setColumnType()
			debug.Print(err.Error())
			return r, nil
		}
		rows := make([]string, len(row))
		for i, col := range row {
			rows[i] = col
			// If there are more columns than header, add column names.
			if len(r.names) < i+1 {
				r.names = append(r.names, "c"+strconv.Itoa(i+1))
			}
		}
		r.preRead = append(r.preRead, rows)
	}
	r.uniform = uniformLayout(r.names, r.preRead)
	r.setColumnType()
	return r, nil
}

func NewTSVReader(reader io.Reader, opts *ReadOpts) (*CSVReader, error) {
	opts.InDelimiter = "\t"
	return NewCSVReader(reader, opts)
}

func NewPSVReader(reader io.Reader, opts *ReadOpts) (*CSVReader, error) {
	opts.InDelimiter = "|"
	return NewCSVReader(reader, opts)
}

func (r *CSVReader) setColumnType() {
	if r.names == nil {
		return
	}
	r.types = make([]string, len(r.names))
	for i := 0; i < len(r.names); i++ {
		r.types[i] = DefaultDBType
	}
}

func delimiter(sepString string) (rune, error) {
	if sepString == "" {
		return 0, nil
	}
	sepRunes, err := strconv.Unquote(`'` + sepString + `'`)
	if err != nil {
		return ',', fmt.Errorf("can not get separator: %w:\"%s\"", err, sepString)
	}
	sepRune := ([]rune(sepRunes))[0]
	return sepRune, err
}

// Names returns column names.
func (r *CSVReader) Names() ([]string, error) {
	if len(r.names) == 0 {
		return r.names, ErrNoRows
	}
	return r.names, nil
}

// Types returns column types.
// All CSV types return the DefaultDBType.
func (r *CSVReader) Types() ([]string, error) {
	if len(r.types) == 0 {
		return r.types, ErrNoRows
	}
	return r.types, nil
}

// PreReadRow is returns only columns that store preread rows.
func (r *CSVReader) PreReadRow() [][]any {
	rowNum := len(r.preRead)
	rows := make([][]any, rowNum)
	for n := 0; n < rowNum; n++ {
		rows[n] = make([]any, len(r.names))
		for i, f := range r.preRead[n] {
			rows[n][i] = f
			if r.needNULL {
				rows[n][i] = replaceNULL(r.inNULL, rows[n][i])
			}
		}
	}
	return rows
}

// ReadRow is read the rest of the row.
func (r *CSVReader) ReadRow(row []any) ([]any, error) {
	if r.limitRead {
		return nil, io.EOF
	}
	record, err := r.reader.Read()
	if err != nil {
		return row, err
	}
	if r.uniform {
		// The layout was fixed by the pre-read rows, so the record can
		// be copied straight into the row.
		fillRow(row, record)
		if r.needNULL {
			for i := range row {
				row[i] = replaceNULL(r.inNULL, row[i])
			}
		}
		return row, nil
	}
	for i := 0; len(row) > i; i++ {
		if len(record) > i {
			row[i] = record[i]
			if r.needNULL {
				row[i] = replaceNULL(r.inNULL, row[i])
			}
		} else {
			row[i] = nil
		}
	}
	return row, nil
}

// fillRow copies a record from a uniform producer into row. Such
// records always carry the full column width, so the fields are moved
// in one pass without per-field reconciliation.
func fillRow(row []any, record []string) {
	for i := range row {
		row[i] = record[i]
	}
}
