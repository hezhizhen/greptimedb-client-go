// Copyright 2023 Greptime Team
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
// http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package greptime

import (
	"errors"
	"fmt"
	"io"
	"math"
	"time"

	greptimepb "github.com/GreptimeTeam/greptime-proto/go/greptime/v1"
	"github.com/apache/arrow/go/arrow"
	"github.com/apache/arrow/go/arrow/array"
	"github.com/apache/arrow/go/arrow/flight"
)

// Metric represents multiple rows of data, and also Metric can specify
// the timestamp column name and precision
type Metric struct {
	timestampAlias     string
	timestampPrecision time.Duration
	// orders and columns SHOULD NOT contain timestampAlias key
	orders  []string
	columns map[string]column

	series []Series
}

// GetTagsAndFields get all column names from metric, except timestamp column
func (m *Metric) GetTagsAndFields() []string {
	dst := make([]string, len(m.orders))
	copy(dst, m.orders)
	return dst
}

// GetSeries gets all data from metric
func (m *Metric) GetSeries() []Series {
	return m.series
}

func buildMetricFromReader(r *flight.Reader) (*Metric, error) {
	metric := Metric{}

	if r == nil {
		return nil, errors.New("Internal Error, empty reader pointer")
	}

	record, err := r.Reader.Read()
	if err == io.EOF {
		return &metric, nil
	}
	if err != nil {
		return nil, err
	}

	fields := r.Schema().Fields()
	timestampIndex := extractTimestampIndex(fields)

	precision := time.Millisecond
	if timestampIndex != -1 {
		precision, err = extractPrecision(&fields[timestampIndex])
		if err != nil {
			return nil, err
		}

		if err = metric.SetTimestampAlias(fields[timestampIndex].Name); err != nil {
			return nil, err
		}
	}
	metric.SetTimePrecision(precision)

	for i := 0; i < int(record.NumRows()); i++ {
		series := Series{}
		for j := 0; j < int(record.NumCols()); j++ {
			column := record.Column(j)
			colVal, err := fromColumn(column, i)
			if err != nil {
				return nil, err
			}
			if j == timestampIndex {
				series.SetTimestamp(colVal.(time.Time))
			} else {
				series.AddField(fields[j].Name, colVal)
			}
		}
		metric.AddSeries(series)
	}

	return &metric, nil
}

func extractTimestampIndex(fields []arrow.Field) int {
	for i, field := range fields {
		if res := field.Metadata.FindKey("greptime:time_index"); res != -1 {
			if field.Metadata.Values()[res] == "true" {
				return i
			}
		}
	}
	return -1
}

func extractPrecision(field *arrow.Field) (time.Duration, error) {
	if field == nil {
		return 0, errors.New("field should not be empty")
	}
	dataType, ok := field.Type.(*arrow.TimestampType)
	if !ok {
		return 0, fmt.Errorf("unsupported arrow field type '%s'", field.Type.Name())
	}
	switch dataType.Unit {
	case arrow.Microsecond:
		return time.Microsecond, nil
	case arrow.Millisecond:
		return time.Millisecond, nil
	case arrow.Second:
		return time.Second, nil
	case arrow.Nanosecond:
		return time.Nanosecond, nil
	default:
		return 0, fmt.Errorf("unsupported arrow type '%s'", field.Type.Name())
	}

}

// retrive arrow value from the column at idx position
func fromColumn(column array.Interface, idx int) (any, error) {
	if column.IsNull(idx) {
		return nil, nil
	}
	switch typedColumn := column.(type) {
	case *array.Int64:
		return typedColumn.Value(idx), nil
	case *array.Int32:
		return typedColumn.Value(idx), nil
	case *array.Uint64:
		return typedColumn.Value(idx), nil
	case *array.Uint32:
		return typedColumn.Value(idx), nil
	case *array.Float64:
		return typedColumn.Value(idx), nil
	case *array.String:
		return typedColumn.Value(idx), nil
	case *array.Boolean:
		return typedColumn.Value(idx), nil
	case *array.Timestamp:
		dataType, ok := column.DataType().(*arrow.TimestampType)
		if !ok {
			return nil, fmt.Errorf("unsupported arrow type '%T' for '%s'", typedColumn, column.DataType().Name())
		}
		value := int64(typedColumn.Value(idx))
		switch dataType.Unit {
		case arrow.Microsecond:
			return time.UnixMicro(value), nil
		case arrow.Millisecond:
			return time.UnixMilli(value), nil
		case arrow.Second:
			return time.Unix(value, 0), nil
		case arrow.Nanosecond:
			return time.Unix(0, value), nil
		default:
			return nil, fmt.Errorf("unsupported arrow type '%T' for '%s'", typedColumn, column.DataType().Name())
		}
	default:
		return nil, fmt.Errorf("unsupported arrow type '%T' for '%s'", typedColumn, column.DataType().Name())
	}
}

// SetTimePrecision set precsion for Metric. Valid durations include:
//   - time.Nanosecond
//   - time.Microsecond
//   - time.Millisecond
//   - time.Second.
//
// # Pay attention
//
//   - once the precision has been set, it can not be changed
//   - insert will fail if precision does not match with the existing precision of the schema in greptimedb
func (m *Metric) SetTimePrecision(precision time.Duration) error {
	if !isValidPrecision(precision) {
		return ErrInvalidTimePrecision
	}
	m.timestampPrecision = precision
	return nil
}

// SetTimestampAlias helps to specify the timestamp column name, default is ts.
func (m *Metric) SetTimestampAlias(alias string) error {
	alias, err := toColumnName(alias)
	if err != nil {
		return err
	}
	m.timestampAlias = alias
	return nil
}

// GetTimestampAlias get the timestamp column name, default is ts.
func (m *Metric) GetTimestampAlias() string {
	if len(m.timestampAlias) == 0 {
		return "ts"
	}
	return m.timestampAlias
}

// AddSeries add one row to Metric.
//
// # Pay Attention
//
//   - different row can have different fields, Metric will union all the columns,
//     leave empty value of one row if the column is not specified in this row
//   - same column name MUST have same schema, which means Tag,Field,Timestamp and
//     data type MUST BE the same of the same column name in different rows
func (m *Metric) AddSeries(s Series) error {
	if s.timestamp.IsZero() {
		return ErrEmptyTimestamp
	}

	if m.columns == nil {
		m.columns = map[string]column{}
	}

	if m.orders == nil {
		m.orders = []string{}
	}

	if m.series == nil {
		m.series = []Series{}
	}

	for _, key := range s.orders {
		sCol := s.columns[key]
		if mCol, seen := m.columns[key]; seen {
			if err := checkColumnEquality(key, mCol, sCol); err != nil {
				return err
			}
		} else {
			m.orders = append(m.orders, key)
			m.columns[key] = sCol
		}
	}

	m.series = append(m.series, s)
	return nil
}

func (m *Metric) intoGreptimeColumn() ([]*greptimepb.Column, error) {
	if len(m.series) == 0 {
		return nil, ErrNoSeriesInMetric
	}

	result, err := m.intoDataColumns()
	if err != nil {
		return nil, err
	}

	tsColumn, err := m.intoTimestampColumn()
	if err != nil {
		return nil, err
	}

	return append(result, tsColumn), nil
}

// nullMaskByteSize helps to calculate how many bytes needed in Mask.shrink
func (m *Metric) nullMaskByteSize() int {
	return int(math.Ceil(float64(len(m.series)) / 8.0))
}

// intoDataColumns does not contain timestamp semantic column
func (m *Metric) intoDataColumns() ([]*greptimepb.Column, error) {
	nullMasks := map[string]*mask{}
	mappedCols := map[string]*greptimepb.Column{}
	for name, col := range m.columns {
		column := greptimepb.Column{
			ColumnName:   name,
			SemanticType: col.semantic,
			Datatype:     col.typ,
			Values:       &greptimepb.Column_Values{},
			NullMask:     nil,
		}
		mappedCols[name] = &column
	}

	for rowIdx, s := range m.series {
		for name, col := range mappedCols {
			if val, exist := s.vals[name]; exist {
				if err := setColumn(col, val); err != nil {
					return nil, err
				}
			} else {
				nullMask, exist := nullMasks[name]
				if !exist {
					nullMask = &mask{}
					nullMasks[name] = nullMask
				}
				nullMask.set(uint(rowIdx))
			}
		}
	}

	if len(nullMasks) > 0 {
		if err := setNullMask(mappedCols, nullMasks, m.nullMaskByteSize()); err != nil {
			return nil, err
		}
	}

	result := make([]*greptimepb.Column, 0, len(mappedCols))
	for _, key := range m.orders {
		result = append(result, mappedCols[key])
	}

	return result, nil
}

func (m *Metric) intoTimestampColumn() (*greptimepb.Column, error) {
	datatype, err := precisionToDataType(m.timestampPrecision)
	if err != nil {
		return nil, err
	}
	tsColumn := &greptimepb.Column{
		ColumnName:   m.GetTimestampAlias(),
		SemanticType: greptimepb.Column_TIMESTAMP,
		Datatype:     datatype,
		Values:       &greptimepb.Column_Values{},
		NullMask:     nil,
	}
	nullMask := mask{}
	for _, s := range m.series {
		switch datatype {
		case greptimepb.ColumnDataType_TIMESTAMP_SECOND:
			setColumn(tsColumn, s.timestamp.Unix())
		case greptimepb.ColumnDataType_TIMESTAMP_MICROSECOND:
			setColumn(tsColumn, s.timestamp.UnixMicro())
		case greptimepb.ColumnDataType_TIMESTAMP_NANOSECOND:
			setColumn(tsColumn, s.timestamp.UnixNano())
		default: // greptimepb.ColumnDataType_TIMESTAMP_MILLISECOND
			setColumn(tsColumn, s.timestamp.UnixMilli())
		}
	}

	if b, err := nullMask.shrink(m.nullMaskByteSize()); err != nil {
		return nil, err
	} else {
		tsColumn.NullMask = b
	}

	return tsColumn, nil
}

func setColumn(col *greptimepb.Column, val any) error {
	switch col.Datatype {
	case greptimepb.ColumnDataType_INT8:
		col.Values.I8Values = append(col.Values.I8Values, int32(val.(int8)))
	case greptimepb.ColumnDataType_INT16:
		col.Values.I16Values = append(col.Values.I16Values, int32(val.(int16)))
	case greptimepb.ColumnDataType_INT32:
		col.Values.I32Values = append(col.Values.I32Values, val.(int32))
	case greptimepb.ColumnDataType_INT64:
		col.Values.I64Values = append(col.Values.I64Values, val.(int64))
	case greptimepb.ColumnDataType_UINT8:
		col.Values.U8Values = append(col.Values.U8Values, uint32(val.(uint8)))
	case greptimepb.ColumnDataType_UINT16:
		col.Values.U16Values = append(col.Values.U16Values, uint32(val.(uint16)))
	case greptimepb.ColumnDataType_UINT32:
		col.Values.U32Values = append(col.Values.U32Values, val.(uint32))
	case greptimepb.ColumnDataType_UINT64:
		col.Values.U64Values = append(col.Values.U64Values, val.(uint64))
	case greptimepb.ColumnDataType_FLOAT32:
		col.Values.F32Values = append(col.Values.F32Values, val.(float32))
	case greptimepb.ColumnDataType_FLOAT64:
		col.Values.F64Values = append(col.Values.F64Values, val.(float64))
	case greptimepb.ColumnDataType_BOOLEAN:
		col.Values.BoolValues = append(col.Values.BoolValues, val.(bool))
	case greptimepb.ColumnDataType_STRING:
		col.Values.StringValues = append(col.Values.StringValues, val.(string))
	case greptimepb.ColumnDataType_BINARY:
		col.Values.BinaryValues = append(col.Values.BinaryValues, val.([]byte))
	case greptimepb.ColumnDataType_TIMESTAMP_SECOND:
		col.Values.TsSecondValues = append(col.Values.TsSecondValues, val.(int64))
	case greptimepb.ColumnDataType_TIMESTAMP_MILLISECOND:
		col.Values.TsMillisecondValues = append(col.Values.TsMillisecondValues, val.(int64))
	case greptimepb.ColumnDataType_TIMESTAMP_MICROSECOND:
		col.Values.TsMicrosecondValues = append(col.Values.TsMicrosecondValues, val.(int64))
	case greptimepb.ColumnDataType_TIMESTAMP_NANOSECOND:
		col.Values.TsNanosecondValues = append(col.Values.TsNanosecondValues, val.(int64))
	default:
		return fmt.Errorf("unknown column data type: %v", col.Datatype)
	}
	return nil
}

func setNullMask(cols map[string]*greptimepb.Column, masks map[string]*mask, size int) error {
	for name, mask := range masks {
		b, err := mask.shrink(size)
		if err != nil {
			return err
		}

		col, exist := cols[name]
		if !exist {
			return fmt.Errorf("'%s' column not found when set null mask", name)
		}
		col.NullMask = b
	}

	return nil
}
