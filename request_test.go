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
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestQueryBuildGreptimeRequest(t *testing.T) {
	rb := &QueryRequest{}
	request, err := rb.buildGreptimeRequest(&Config{})
	assert.Nil(t, request)
	assert.ErrorIs(t, err, ErrEmptyDatabase)

	rb.WithDatabase("disk_usage")
	request, err = rb.buildGreptimeRequest(&Config{})
	assert.Nil(t, request)
	assert.ErrorIs(t, err, ErrEmptyQuery)

	// test Sql
	rb.WithSql("select * from monitor")
	request, err = rb.buildGreptimeRequest(&Config{})
	assert.NotNil(t, request)
	assert.Nil(t, err)

	// test instant promql
	rb.WithInstantPromql(NewInstantPromql("up == 0"))
	request, err = rb.buildGreptimeRequest(&Config{})
	assert.Nil(t, request)
	assert.ErrorIs(t, err, ErrNotImplemented)

	// test range promql
	rp := &RangePromql{
		Query: "up == 0",
		Start: time.Now(),
		End:   time.Now(),
		Step:  time.Second * 10,
	}
	rb.WithRangePromql(rp)
	request, err = rb.buildGreptimeRequest(&Config{})
	assert.NotNil(t, request)
	assert.Nil(t, err)
}

func TestQueryBuildPromqlRequest(t *testing.T) {
	rb := &QueryRequest{}
	request, err := rb.buildPromqlRequest(&Config{})
	assert.Nil(t, request)
	assert.ErrorIs(t, err, ErrEmptyDatabase)

	rb.WithDatabase("disk_usage")
	request, err = rb.buildPromqlRequest(&Config{})
	assert.Nil(t, request)
	assert.ErrorIs(t, err, ErrEmptyQuery)

	// test Sql
	rb.WithSql("select * from monitor")
	request, err = rb.buildPromqlRequest(&Config{})
	assert.Nil(t, request)
	assert.ErrorIs(t, err, ErrSqlInPromql)

	// test instant promql
	rb.WithInstantPromql(NewInstantPromql("up == 0"))
	request, err = rb.buildPromqlRequest(&Config{})
	assert.NotNil(t, request)
	assert.Nil(t, err)

	// test range promql
	rp := &RangePromql{
		Query: "up == 0",
		Start: time.Now(),
		End:   time.Now(),
		Step:  time.Second * 10,
	}
	rb.WithRangePromql(rp)
	request, err = rb.buildPromqlRequest(&Config{})
	assert.NotNil(t, request)
	assert.Nil(t, err)
}

func TestInsertBuilder(t *testing.T) {
	r := InsertRequest{}

	// empty database
	req, err := r.build(&Config{})
	assert.Equal(t, ErrEmptyDatabase, err)
	assert.Nil(t, req)

	// empty table
	r.header = header{"public"}
	req, err = r.build(&Config{})
	assert.Equal(t, ErrEmptyTable, err)
	assert.Nil(t, req)

	// empty series
	r.WithTable("monitor")
	req, err = r.build(&Config{})
	assert.Equal(t, ErrNoSeriesInMetric, err)
	assert.Nil(t, req)
}
