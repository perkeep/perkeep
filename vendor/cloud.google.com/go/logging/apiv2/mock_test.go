// Copyright 2016, Google Inc. All rights reserved.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

// AUTO-GENERATED CODE. DO NOT EDIT.

package logging

import (
	google_protobuf "github.com/golang/protobuf/ptypes/empty"
	loggingpb "google.golang.org/genproto/googleapis/logging/v2"
)

import (
	"flag"
	"io"
	"log"
	"net"
	"os"
	"reflect"
	"testing"

	"golang.org/x/net/context"
	"google.golang.org/api/option"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
)

var _ = io.EOF

type mockLoggingServer struct {
	reqs []interface{}

	// If set, all calls return this error.
	err error

	// responses to return if err == nil
	resps []interface{}
}

func (s *mockLoggingServer) DeleteLog(_ context.Context, req *loggingpb.DeleteLogRequest) (*google_protobuf.Empty, error) {
	s.reqs = append(s.reqs, req)
	if s.err != nil {
		return nil, s.err
	}
	return s.resps[0].(*google_protobuf.Empty), nil
}

func (s *mockLoggingServer) WriteLogEntries(_ context.Context, req *loggingpb.WriteLogEntriesRequest) (*loggingpb.WriteLogEntriesResponse, error) {
	s.reqs = append(s.reqs, req)
	if s.err != nil {
		return nil, s.err
	}
	return s.resps[0].(*loggingpb.WriteLogEntriesResponse), nil
}

func (s *mockLoggingServer) ListLogEntries(_ context.Context, req *loggingpb.ListLogEntriesRequest) (*loggingpb.ListLogEntriesResponse, error) {
	s.reqs = append(s.reqs, req)
	if s.err != nil {
		return nil, s.err
	}
	return s.resps[0].(*loggingpb.ListLogEntriesResponse), nil
}

func (s *mockLoggingServer) ListMonitoredResourceDescriptors(_ context.Context, req *loggingpb.ListMonitoredResourceDescriptorsRequest) (*loggingpb.ListMonitoredResourceDescriptorsResponse, error) {
	s.reqs = append(s.reqs, req)
	if s.err != nil {
		return nil, s.err
	}
	return s.resps[0].(*loggingpb.ListMonitoredResourceDescriptorsResponse), nil
}

type mockConfigServer struct {
	reqs []interface{}

	// If set, all calls return this error.
	err error

	// responses to return if err == nil
	resps []interface{}
}

func (s *mockConfigServer) ListSinks(_ context.Context, req *loggingpb.ListSinksRequest) (*loggingpb.ListSinksResponse, error) {
	s.reqs = append(s.reqs, req)
	if s.err != nil {
		return nil, s.err
	}
	return s.resps[0].(*loggingpb.ListSinksResponse), nil
}

func (s *mockConfigServer) GetSink(_ context.Context, req *loggingpb.GetSinkRequest) (*loggingpb.LogSink, error) {
	s.reqs = append(s.reqs, req)
	if s.err != nil {
		return nil, s.err
	}
	return s.resps[0].(*loggingpb.LogSink), nil
}

func (s *mockConfigServer) CreateSink(_ context.Context, req *loggingpb.CreateSinkRequest) (*loggingpb.LogSink, error) {
	s.reqs = append(s.reqs, req)
	if s.err != nil {
		return nil, s.err
	}
	return s.resps[0].(*loggingpb.LogSink), nil
}

func (s *mockConfigServer) UpdateSink(_ context.Context, req *loggingpb.UpdateSinkRequest) (*loggingpb.LogSink, error) {
	s.reqs = append(s.reqs, req)
	if s.err != nil {
		return nil, s.err
	}
	return s.resps[0].(*loggingpb.LogSink), nil
}

func (s *mockConfigServer) DeleteSink(_ context.Context, req *loggingpb.DeleteSinkRequest) (*google_protobuf.Empty, error) {
	s.reqs = append(s.reqs, req)
	if s.err != nil {
		return nil, s.err
	}
	return s.resps[0].(*google_protobuf.Empty), nil
}

type mockMetricsServer struct {
	reqs []interface{}

	// If set, all calls return this error.
	err error

	// responses to return if err == nil
	resps []interface{}
}

func (s *mockMetricsServer) ListLogMetrics(_ context.Context, req *loggingpb.ListLogMetricsRequest) (*loggingpb.ListLogMetricsResponse, error) {
	s.reqs = append(s.reqs, req)
	if s.err != nil {
		return nil, s.err
	}
	return s.resps[0].(*loggingpb.ListLogMetricsResponse), nil
}

func (s *mockMetricsServer) GetLogMetric(_ context.Context, req *loggingpb.GetLogMetricRequest) (*loggingpb.LogMetric, error) {
	s.reqs = append(s.reqs, req)
	if s.err != nil {
		return nil, s.err
	}
	return s.resps[0].(*loggingpb.LogMetric), nil
}

func (s *mockMetricsServer) CreateLogMetric(_ context.Context, req *loggingpb.CreateLogMetricRequest) (*loggingpb.LogMetric, error) {
	s.reqs = append(s.reqs, req)
	if s.err != nil {
		return nil, s.err
	}
	return s.resps[0].(*loggingpb.LogMetric), nil
}

func (s *mockMetricsServer) UpdateLogMetric(_ context.Context, req *loggingpb.UpdateLogMetricRequest) (*loggingpb.LogMetric, error) {
	s.reqs = append(s.reqs, req)
	if s.err != nil {
		return nil, s.err
	}
	return s.resps[0].(*loggingpb.LogMetric), nil
}

func (s *mockMetricsServer) DeleteLogMetric(_ context.Context, req *loggingpb.DeleteLogMetricRequest) (*google_protobuf.Empty, error) {
	s.reqs = append(s.reqs, req)
	if s.err != nil {
		return nil, s.err
	}
	return s.resps[0].(*google_protobuf.Empty), nil
}

// clientOpt is the option tests should use to connect to the test server.
// It is initialized by TestMain.
var clientOpt option.ClientOption

var (
	mockLogging mockLoggingServer
	mockConfig  mockConfigServer
	mockMetrics mockMetricsServer
)

func TestMain(m *testing.M) {
	flag.Parse()

	serv := grpc.NewServer()
	loggingpb.RegisterLoggingServiceV2Server(serv, &mockLogging)
	loggingpb.RegisterConfigServiceV2Server(serv, &mockConfig)
	loggingpb.RegisterMetricsServiceV2Server(serv, &mockMetrics)

	lis, err := net.Listen("tcp", "localhost:0")
	if err != nil {
		log.Fatal(err)
	}
	go serv.Serve(lis)

	conn, err := grpc.Dial(lis.Addr().String(), grpc.WithInsecure())
	if err != nil {
		log.Fatal(err)
	}
	clientOpt = option.WithGRPCConn(conn)

	os.Exit(m.Run())
}

func TestLoggingServiceV2DeleteLogError(t *testing.T) {
	errCode := codes.Internal
	mockLogging.err = grpc.Errorf(errCode, "test error")

	c, err := NewClient(context.Background(), clientOpt)
	if err != nil {
		t.Fatal(err)
	}

	var req *loggingpb.DeleteLogRequest

	reflect.ValueOf(&req).Elem().Set(reflect.New(reflect.TypeOf(req).Elem()))

	err = c.DeleteLog(context.Background(), req)

	if c := grpc.Code(err); c != errCode {
		t.Errorf("got error code %q, want %q", c, errCode)
	}
}
func TestLoggingServiceV2WriteLogEntriesError(t *testing.T) {
	errCode := codes.Internal
	mockLogging.err = grpc.Errorf(errCode, "test error")

	c, err := NewClient(context.Background(), clientOpt)
	if err != nil {
		t.Fatal(err)
	}

	var req *loggingpb.WriteLogEntriesRequest

	reflect.ValueOf(&req).Elem().Set(reflect.New(reflect.TypeOf(req).Elem()))

	_, err = c.WriteLogEntries(context.Background(), req)

	if c := grpc.Code(err); c != errCode {
		t.Errorf("got error code %q, want %q", c, errCode)
	}
}
func TestLoggingServiceV2ListLogEntriesError(t *testing.T) {
	errCode := codes.Internal
	mockLogging.err = grpc.Errorf(errCode, "test error")

	c, err := NewClient(context.Background(), clientOpt)
	if err != nil {
		t.Fatal(err)
	}

	var req *loggingpb.ListLogEntriesRequest

	reflect.ValueOf(&req).Elem().Set(reflect.New(reflect.TypeOf(req).Elem()))

	_, err = c.ListLogEntries(context.Background(), req).Next()

	if c := grpc.Code(err); c != errCode {
		t.Errorf("got error code %q, want %q", c, errCode)
	}
}
func TestLoggingServiceV2ListMonitoredResourceDescriptorsError(t *testing.T) {
	errCode := codes.Internal
	mockLogging.err = grpc.Errorf(errCode, "test error")

	c, err := NewClient(context.Background(), clientOpt)
	if err != nil {
		t.Fatal(err)
	}

	var req *loggingpb.ListMonitoredResourceDescriptorsRequest

	reflect.ValueOf(&req).Elem().Set(reflect.New(reflect.TypeOf(req).Elem()))

	_, err = c.ListMonitoredResourceDescriptors(context.Background(), req).Next()

	if c := grpc.Code(err); c != errCode {
		t.Errorf("got error code %q, want %q", c, errCode)
	}
}
func TestConfigServiceV2ListSinksError(t *testing.T) {
	errCode := codes.Internal
	mockConfig.err = grpc.Errorf(errCode, "test error")

	c, err := NewConfigClient(context.Background(), clientOpt)
	if err != nil {
		t.Fatal(err)
	}

	var req *loggingpb.ListSinksRequest

	reflect.ValueOf(&req).Elem().Set(reflect.New(reflect.TypeOf(req).Elem()))

	_, err = c.ListSinks(context.Background(), req).Next()

	if c := grpc.Code(err); c != errCode {
		t.Errorf("got error code %q, want %q", c, errCode)
	}
}
func TestConfigServiceV2GetSinkError(t *testing.T) {
	errCode := codes.Internal
	mockConfig.err = grpc.Errorf(errCode, "test error")

	c, err := NewConfigClient(context.Background(), clientOpt)
	if err != nil {
		t.Fatal(err)
	}

	var req *loggingpb.GetSinkRequest

	reflect.ValueOf(&req).Elem().Set(reflect.New(reflect.TypeOf(req).Elem()))

	_, err = c.GetSink(context.Background(), req)

	if c := grpc.Code(err); c != errCode {
		t.Errorf("got error code %q, want %q", c, errCode)
	}
}
func TestConfigServiceV2CreateSinkError(t *testing.T) {
	errCode := codes.Internal
	mockConfig.err = grpc.Errorf(errCode, "test error")

	c, err := NewConfigClient(context.Background(), clientOpt)
	if err != nil {
		t.Fatal(err)
	}

	var req *loggingpb.CreateSinkRequest

	reflect.ValueOf(&req).Elem().Set(reflect.New(reflect.TypeOf(req).Elem()))

	_, err = c.CreateSink(context.Background(), req)

	if c := grpc.Code(err); c != errCode {
		t.Errorf("got error code %q, want %q", c, errCode)
	}
}
func TestConfigServiceV2UpdateSinkError(t *testing.T) {
	errCode := codes.Internal
	mockConfig.err = grpc.Errorf(errCode, "test error")

	c, err := NewConfigClient(context.Background(), clientOpt)
	if err != nil {
		t.Fatal(err)
	}

	var req *loggingpb.UpdateSinkRequest

	reflect.ValueOf(&req).Elem().Set(reflect.New(reflect.TypeOf(req).Elem()))

	_, err = c.UpdateSink(context.Background(), req)

	if c := grpc.Code(err); c != errCode {
		t.Errorf("got error code %q, want %q", c, errCode)
	}
}
func TestConfigServiceV2DeleteSinkError(t *testing.T) {
	errCode := codes.Internal
	mockConfig.err = grpc.Errorf(errCode, "test error")

	c, err := NewConfigClient(context.Background(), clientOpt)
	if err != nil {
		t.Fatal(err)
	}

	var req *loggingpb.DeleteSinkRequest

	reflect.ValueOf(&req).Elem().Set(reflect.New(reflect.TypeOf(req).Elem()))

	err = c.DeleteSink(context.Background(), req)

	if c := grpc.Code(err); c != errCode {
		t.Errorf("got error code %q, want %q", c, errCode)
	}
}
func TestMetricsServiceV2ListLogMetricsError(t *testing.T) {
	errCode := codes.Internal
	mockMetrics.err = grpc.Errorf(errCode, "test error")

	c, err := NewMetricsClient(context.Background(), clientOpt)
	if err != nil {
		t.Fatal(err)
	}

	var req *loggingpb.ListLogMetricsRequest

	reflect.ValueOf(&req).Elem().Set(reflect.New(reflect.TypeOf(req).Elem()))

	_, err = c.ListLogMetrics(context.Background(), req).Next()

	if c := grpc.Code(err); c != errCode {
		t.Errorf("got error code %q, want %q", c, errCode)
	}
}
func TestMetricsServiceV2GetLogMetricError(t *testing.T) {
	errCode := codes.Internal
	mockMetrics.err = grpc.Errorf(errCode, "test error")

	c, err := NewMetricsClient(context.Background(), clientOpt)
	if err != nil {
		t.Fatal(err)
	}

	var req *loggingpb.GetLogMetricRequest

	reflect.ValueOf(&req).Elem().Set(reflect.New(reflect.TypeOf(req).Elem()))

	_, err = c.GetLogMetric(context.Background(), req)

	if c := grpc.Code(err); c != errCode {
		t.Errorf("got error code %q, want %q", c, errCode)
	}
}
func TestMetricsServiceV2CreateLogMetricError(t *testing.T) {
	errCode := codes.Internal
	mockMetrics.err = grpc.Errorf(errCode, "test error")

	c, err := NewMetricsClient(context.Background(), clientOpt)
	if err != nil {
		t.Fatal(err)
	}

	var req *loggingpb.CreateLogMetricRequest

	reflect.ValueOf(&req).Elem().Set(reflect.New(reflect.TypeOf(req).Elem()))

	_, err = c.CreateLogMetric(context.Background(), req)

	if c := grpc.Code(err); c != errCode {
		t.Errorf("got error code %q, want %q", c, errCode)
	}
}
func TestMetricsServiceV2UpdateLogMetricError(t *testing.T) {
	errCode := codes.Internal
	mockMetrics.err = grpc.Errorf(errCode, "test error")

	c, err := NewMetricsClient(context.Background(), clientOpt)
	if err != nil {
		t.Fatal(err)
	}

	var req *loggingpb.UpdateLogMetricRequest

	reflect.ValueOf(&req).Elem().Set(reflect.New(reflect.TypeOf(req).Elem()))

	_, err = c.UpdateLogMetric(context.Background(), req)

	if c := grpc.Code(err); c != errCode {
		t.Errorf("got error code %q, want %q", c, errCode)
	}
}
func TestMetricsServiceV2DeleteLogMetricError(t *testing.T) {
	errCode := codes.Internal
	mockMetrics.err = grpc.Errorf(errCode, "test error")

	c, err := NewMetricsClient(context.Background(), clientOpt)
	if err != nil {
		t.Fatal(err)
	}

	var req *loggingpb.DeleteLogMetricRequest

	reflect.ValueOf(&req).Elem().Set(reflect.New(reflect.TypeOf(req).Elem()))

	err = c.DeleteLogMetric(context.Background(), req)

	if c := grpc.Code(err); c != errCode {
		t.Errorf("got error code %q, want %q", c, errCode)
	}
}
