package command

import (
	"bytes"
	"compress/gzip"
	"expvar"
	"fmt"
	"io"

	"github.com/tarungka/wire/internal/command/proto"
	pb "google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/runtime/protoimpl"
)

const (
	// Verify that this generated code is sufficiently up-to-date.
	_ = protoimpl.EnforceVersion(20 - protoimpl.MinVersion)
	// Verify that runtime/protoimpl is sufficiently up-to-date.
	_ = protoimpl.EnforceVersion(protoimpl.MaxVersion - 20)
)

type QueryRequest_Level int32

const (
	QueryRequest_QUERY_REQUEST_LEVEL_NONE   QueryRequest_Level = 0
	QueryRequest_QUERY_REQUEST_LEVEL_WEAK   QueryRequest_Level = 1
	QueryRequest_QUERY_REQUEST_LEVEL_STRONG QueryRequest_Level = 2
	QueryRequest_QUERY_REQUEST_LEVEL_AUTO   QueryRequest_Level = 3
)

const (
	defaultBatchThreshold = 50
	defaultSizeThreshold  = 1024
)

// Requester is the interface objects must support to be marshaled
// successfully.
type Requester interface {
	pb.Message
	GetRequest() *proto.Request
}

// RequestMarshaler marshals Request objects, potentially performing
// gzip compression.
type RequestMarshaler struct {
	BatchThreshold   int
	SizeThreshold    int
	ForceCompression bool
}

const (
	numRequests             = "num_requests"
	numCompressedRequests   = "num_compressed_requests"
	numUncompressedRequests = "num_uncompressed_requests"
	numCompressedBytes      = "num_compressed_bytes"
	numPrecompressedBytes   = "num_precompressed_bytes"
	numUncompressedBytes    = "num_uncompressed_bytes"
	numCompressionMisses    = "num_compression_misses"
)

// stats captures stats for the Proto marshaler.
var stats *expvar.Map

func init() {
	stats = expvar.NewMap("proto")
	stats.Add(numRequests, 0)
	stats.Add(numCompressedRequests, 0)
	stats.Add(numUncompressedRequests, 0)
	stats.Add(numCompressedBytes, 0)
	stats.Add(numUncompressedBytes, 0)
	stats.Add(numCompressionMisses, 0)
	stats.Add(numPrecompressedBytes, 0)
}

// NewRequestMarshaler returns an initialized RequestMarshaler.
func NewRequestMarshaler() *RequestMarshaler {
	return &RequestMarshaler{
		BatchThreshold: defaultBatchThreshold,
		SizeThreshold:  defaultSizeThreshold,
	}
}

// Marshal marshals a Requester object, returning a byte slice, a bool
// indicating whether the contents are compressed, or an error.
func (m *RequestMarshaler) Marshal(r Requester) ([]byte, bool, error) {
	stats.Add(numRequests, 1)
	compress := false

	stmts := r.GetRequest().GetStatements()
	if len(stmts) >= m.BatchThreshold {
		compress = true
	} else {
		for i := range stmts {
			if len(stmts[i].Sql) >= m.SizeThreshold {
				compress = true
				break
			}
		}
	}

	b, err := pb.Marshal(r)
	if err != nil {
		return nil, false, err
	}
	ubz := len(b)
	stats.Add(numPrecompressedBytes, int64(ubz))

	if compress {
		// Let's try compression.
		gzData, err := gzCompress(b)
		if err != nil {
			return nil, false, err
		}

		// Is compression better?
		if ubz > len(gzData) || m.ForceCompression {
			// Yes! Let's keep it.
			b = gzData
			stats.Add(numCompressedRequests, 1)
			stats.Add(numCompressedBytes, int64(len(b)))
		} else {
			// No. :-( Dump it.
			compress = false
			stats.Add(numCompressionMisses, 1)
		}
	} else {
		stats.Add(numUncompressedRequests, 1)
		stats.Add(numUncompressedBytes, int64(len(b)))
	}

	return b, compress, nil
}

// Stats returns status and diagnostic information about
// the RequestMarshaler.
func (m *RequestMarshaler) Stats() map[string]interface{} {
	return map[string]interface{}{
		"compression_size":  m.SizeThreshold,
		"compression_batch": m.BatchThreshold,
		"force_compression": m.ForceCompression,
	}
}

// Marshal marshals a Command.
func Marshal(c *proto.Command) ([]byte, error) {
	return pb.Marshal(c)
}

// Unmarshal unmarshals a Command
func Unmarshal(b []byte, c *proto.Command) error {
	return pb.Unmarshal(b, c)
}

// MarshalNoop marshals a Noop command
func MarshalNoop(c *proto.Noop) ([]byte, error) {
	return pb.Marshal(c)
}

// UnmarshalNoop unmarshals a Noop command
func UnmarshalNoop(b []byte, c *proto.Noop) error {
	return pb.Unmarshal(b, c)
}

// MarshalLoadRequest marshals a LoadRequest command
func MarshalLoadRequest(lr *proto.LoadRequest) ([]byte, error) {
	b, err := pb.Marshal(lr)
	if err != nil {
		return nil, err
	}
	return gzCompress(b)
}

// UnmarshalLoadRequest unmarshals a LoadRequest command
func UnmarshalLoadRequest(b []byte, lr *proto.LoadRequest) error {
	u, err := gzUncompress(b)
	if err != nil {
		return err
	}
	return pb.Unmarshal(u, lr)
}

// MarshalLoadChunkRequest marshals a LoadChunkRequest command
func MarshalLoadChunkRequest(lr *proto.LoadChunkRequest) ([]byte, error) {
	return pb.Marshal(lr)
}

// UnmarshalLoadChunkRequest unmarshals a LoadChunkRequest command
func UnmarshalLoadChunkRequest(b []byte, lr *proto.LoadChunkRequest) error {
	return pb.Unmarshal(b, lr)
}

// UnmarshalSubCommand unmarshals a sub command m. It assumes that
// m is the correct type.
func UnmarshalSubCommand(c *proto.Command, m pb.Message) error {
	b := c.SubCommand
	if c.Compressed {
		var err error
		b, err = gzUncompress(b)
		if err != nil {
			return fmt.Errorf("unmarshal sub uncompress: %s", err)
		}
	}

	if err := pb.Unmarshal(b, m); err != nil {
		return fmt.Errorf("proto unmarshal: %s", err)
	}
	return nil
}

// gzCompress compresses the given byte slice.
func gzCompress(b []byte) ([]byte, error) {
	var buf bytes.Buffer
	gzw, err := gzip.NewWriterLevel(&buf, gzip.BestCompression)
	if err != nil {
		return nil, fmt.Errorf("gzip new writer: %s", err)
	}

	if _, err := gzw.Write(b); err != nil {
		return nil, fmt.Errorf("gzip Write: %s", err)
	}
	if err := gzw.Close(); err != nil {
		return nil, fmt.Errorf("gzip Close: %s", err)
	}
	return buf.Bytes(), nil
}

func gzUncompress(b []byte) ([]byte, error) {
	gz, err := gzip.NewReader(bytes.NewReader(b))
	if err != nil {
		return nil, fmt.Errorf("unmarshal gzip NewReader: %s", err)
	}

	ub, err := io.ReadAll(gz)
	if err != nil {
		return nil, fmt.Errorf("unmarshal gzip ReadAll: %s", err)
	}

	if err := gz.Close(); err != nil {
		return nil, fmt.Errorf("unmarshal gzip Close: %s", err)
	}
	return ub, nil
}

type isParameter_Value interface {
	isParameter_Value()
}


// Not yet completely implemented, need to think of all the edge cases
type Parameter struct {
	state         protoimpl.MessageState
	sizeCache     protoimpl.SizeCache
	unknownFields protoimpl.UnknownFields

	// Types that are assignable to Value:
	//
	//	*Parameter_I
	//	*Parameter_D
	//	*Parameter_B
	//	*Parameter_Y
	//	*Parameter_S
	Value isParameter_Value `protobuf_oneof:"value"`
	Name  string            `protobuf:"bytes,6,opt,name=name,proto3" json:"name,omitempty"`
}

// func (x *Parameter) Reset() {
// 	*x = Parameter{}
// 	if protoimpl.UnsafeEnabled {
// 		mi := &file_command_proto_msgTypes[0]
// 		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
// 		ms.StoreMessageInfo(mi)
// 	}
// }

// func (x *Parameter) String() string {
// 	return protoimpl.X.MessageStringOf(x)
// }

// func (*Parameter) ProtoMessage() {}

// func (x *Parameter) ProtoReflect() protoreflect.Message {
// 	mi := &file_command_proto_msgTypes[0]
// 	if protoimpl.UnsafeEnabled && x != nil {
// 		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
// 		if ms.LoadMessageInfo() == nil {
// 			ms.StoreMessageInfo(mi)
// 		}
// 		return ms
// 	}
// 	return mi.MessageOf(x)
// }

// // Deprecated: Use Parameter.ProtoReflect.Descriptor instead.
// func (*Parameter) Descriptor() ([]byte, []int) {
// 	return file_command_proto_rawDescGZIP(), []int{0}
// }

// func (m *Parameter) GetValue() isParameter_Value {
// 	if m != nil {
// 		return m.Value
// 	}
// 	return nil
// }

// func (x *Parameter) GetI() int64 {
// 	if x, ok := x.GetValue().(*Parameter_I); ok {
// 		return x.I
// 	}
// 	return 0
// }

// func (x *Parameter) GetD() float64 {
// 	if x, ok := x.GetValue().(*Parameter_D); ok {
// 		return x.D
// 	}
// 	return 0
// }

// func (x *Parameter) GetB() bool {
// 	if x, ok := x.GetValue().(*Parameter_B); ok {
// 		return x.B
// 	}
// 	return false
// }

// func (x *Parameter) GetY() []byte {
// 	if x, ok := x.GetValue().(*Parameter_Y); ok {
// 		return x.Y
// 	}
// 	return nil
// }

// func (x *Parameter) GetS() string {
// 	if x, ok := x.GetValue().(*Parameter_S); ok {
// 		return x.S
// 	}
// 	return ""
// }

// func (x *Parameter) GetName() string {
// 	if x != nil {
// 		return x.Name
// 	}
// 	return ""
// }

type Statement struct {
	state         protoimpl.MessageState
	sizeCache     protoimpl.SizeCache
	unknownFields protoimpl.UnknownFields

	Sql        string       `protobuf:"bytes,1,opt,name=sql,proto3" json:"sql,omitempty"`
	Parameters []*Parameter `protobuf:"bytes,2,rep,name=parameters,proto3" json:"parameters,omitempty"`
	ForceQuery bool         `protobuf:"varint,3,opt,name=forceQuery,proto3" json:"forceQuery,omitempty"`
}

type Request struct {
	state         protoimpl.MessageState
	sizeCache     protoimpl.SizeCache
	unknownFields protoimpl.UnknownFields

	Transaction bool         `protobuf:"varint,1,opt,name=transaction,proto3" json:"transaction,omitempty"`
	Statements  []*Statement `protobuf:"bytes,2,rep,name=statements,proto3" json:"statements,omitempty"`
	DbTimeout   int64        `protobuf:"varint,3,opt,name=dbTimeout,proto3" json:"dbTimeout,omitempty"`
}

type ExecuteQueryRequest struct {
	state         protoimpl.MessageState
	sizeCache     protoimpl.SizeCache
	unknownFields protoimpl.UnknownFields

	Request         *Request           `protobuf:"bytes,1,opt,name=request,proto3" json:"request,omitempty"`
	Timings         bool               `protobuf:"varint,2,opt,name=timings,proto3" json:"timings,omitempty"`
	Level           QueryRequest_Level `protobuf:"varint,3,opt,name=level,proto3,enum=command.QueryRequest_Level" json:"level,omitempty"`
	Freshness       int64              `protobuf:"varint,4,opt,name=freshness,proto3" json:"freshness,omitempty"`
	FreshnessStrict bool               `protobuf:"varint,5,opt,name=freshness_strict,json=freshnessStrict,proto3" json:"freshness_strict,omitempty"`
}
