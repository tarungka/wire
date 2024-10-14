// Code generated by protoc-gen-go. DO NOT EDIT.
// versions:
// 	protoc-gen-go v1.35.1
// 	protoc        v3.20.3
// source: message.proto

package proto

import (
	proto "github.com/tarungka/wire/internal/command/proto"
	protoreflect "google.golang.org/protobuf/reflect/protoreflect"
	protoimpl "google.golang.org/protobuf/runtime/protoimpl"
	reflect "reflect"
	sync "sync"
)

const (
	// Verify that this generated code is sufficiently up-to-date.
	_ = protoimpl.EnforceVersion(20 - protoimpl.MinVersion)
	// Verify that runtime/protoimpl is sufficiently up-to-date.
	_ = protoimpl.EnforceVersion(protoimpl.MaxVersion - 20)
)

type Command_Type int32

const (
	Command_COMMAND_TYPE_UNKNOWN          Command_Type = 0
	Command_COMMAND_TYPE_GET_NODE_API_URL Command_Type = 1
	Command_COMMAND_TYPE_EXECUTE          Command_Type = 2
	Command_COMMAND_TYPE_QUERY            Command_Type = 3
	Command_COMMAND_TYPE_BACKUP           Command_Type = 4
	Command_COMMAND_TYPE_LOAD             Command_Type = 5
	Command_COMMAND_TYPE_REMOVE_NODE      Command_Type = 6
	Command_COMMAND_TYPE_NOTIFY           Command_Type = 7
	Command_COMMAND_TYPE_JOIN             Command_Type = 8
	Command_COMMAND_TYPE_REQUEST          Command_Type = 9
	Command_COMMAND_TYPE_LOAD_CHUNK       Command_Type = 10
	Command_COMMAND_TYPE_BACKUP_STREAM    Command_Type = 11
)

// Enum value maps for Command_Type.
var (
	Command_Type_name = map[int32]string{
		0:  "COMMAND_TYPE_UNKNOWN",
		1:  "COMMAND_TYPE_GET_NODE_API_URL",
		2:  "COMMAND_TYPE_EXECUTE",
		3:  "COMMAND_TYPE_QUERY",
		4:  "COMMAND_TYPE_BACKUP",
		5:  "COMMAND_TYPE_LOAD",
		6:  "COMMAND_TYPE_REMOVE_NODE",
		7:  "COMMAND_TYPE_NOTIFY",
		8:  "COMMAND_TYPE_JOIN",
		9:  "COMMAND_TYPE_REQUEST",
		10: "COMMAND_TYPE_LOAD_CHUNK",
		11: "COMMAND_TYPE_BACKUP_STREAM",
	}
	Command_Type_value = map[string]int32{
		"COMMAND_TYPE_UNKNOWN":          0,
		"COMMAND_TYPE_GET_NODE_API_URL": 1,
		"COMMAND_TYPE_EXECUTE":          2,
		"COMMAND_TYPE_QUERY":            3,
		"COMMAND_TYPE_BACKUP":           4,
		"COMMAND_TYPE_LOAD":             5,
		"COMMAND_TYPE_REMOVE_NODE":      6,
		"COMMAND_TYPE_NOTIFY":           7,
		"COMMAND_TYPE_JOIN":             8,
		"COMMAND_TYPE_REQUEST":          9,
		"COMMAND_TYPE_LOAD_CHUNK":       10,
		"COMMAND_TYPE_BACKUP_STREAM":    11,
	}
)

func (x Command_Type) Enum() *Command_Type {
	p := new(Command_Type)
	*p = x
	return p
}

func (x Command_Type) String() string {
	return protoimpl.X.EnumStringOf(x.Descriptor(), protoreflect.EnumNumber(x))
}

func (Command_Type) Descriptor() protoreflect.EnumDescriptor {
	return file_message_proto_enumTypes[0].Descriptor()
}

func (Command_Type) Type() protoreflect.EnumType {
	return &file_message_proto_enumTypes[0]
}

func (x Command_Type) Number() protoreflect.EnumNumber {
	return protoreflect.EnumNumber(x)
}

// Deprecated: Use Command_Type.Descriptor instead.
func (Command_Type) EnumDescriptor() ([]byte, []int) {
	return file_message_proto_rawDescGZIP(), []int{2, 0}
}

type Credentials struct {
	state         protoimpl.MessageState
	sizeCache     protoimpl.SizeCache
	unknownFields protoimpl.UnknownFields

	Username string `protobuf:"bytes,1,opt,name=username,proto3" json:"username,omitempty"`
	Password string `protobuf:"bytes,2,opt,name=password,proto3" json:"password,omitempty"`
}

func (x *Credentials) Reset() {
	*x = Credentials{}
	mi := &file_message_proto_msgTypes[0]
	ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
	ms.StoreMessageInfo(mi)
}

func (x *Credentials) String() string {
	return protoimpl.X.MessageStringOf(x)
}

func (*Credentials) ProtoMessage() {}

func (x *Credentials) ProtoReflect() protoreflect.Message {
	mi := &file_message_proto_msgTypes[0]
	if x != nil {
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		if ms.LoadMessageInfo() == nil {
			ms.StoreMessageInfo(mi)
		}
		return ms
	}
	return mi.MessageOf(x)
}

// Deprecated: Use Credentials.ProtoReflect.Descriptor instead.
func (*Credentials) Descriptor() ([]byte, []int) {
	return file_message_proto_rawDescGZIP(), []int{0}
}

func (x *Credentials) GetUsername() string {
	if x != nil {
		return x.Username
	}
	return ""
}

func (x *Credentials) GetPassword() string {
	if x != nil {
		return x.Password
	}
	return ""
}

type NodeMeta struct {
	state         protoimpl.MessageState
	sizeCache     protoimpl.SizeCache
	unknownFields protoimpl.UnknownFields

	Url         string `protobuf:"bytes,1,opt,name=url,proto3" json:"url,omitempty"`
	CommitIndex uint64 `protobuf:"varint,2,opt,name=commit_index,json=commitIndex,proto3" json:"commit_index,omitempty"`
}

func (x *NodeMeta) Reset() {
	*x = NodeMeta{}
	mi := &file_message_proto_msgTypes[1]
	ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
	ms.StoreMessageInfo(mi)
}

func (x *NodeMeta) String() string {
	return protoimpl.X.MessageStringOf(x)
}

func (*NodeMeta) ProtoMessage() {}

func (x *NodeMeta) ProtoReflect() protoreflect.Message {
	mi := &file_message_proto_msgTypes[1]
	if x != nil {
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		if ms.LoadMessageInfo() == nil {
			ms.StoreMessageInfo(mi)
		}
		return ms
	}
	return mi.MessageOf(x)
}

// Deprecated: Use NodeMeta.ProtoReflect.Descriptor instead.
func (*NodeMeta) Descriptor() ([]byte, []int) {
	return file_message_proto_rawDescGZIP(), []int{1}
}

func (x *NodeMeta) GetUrl() string {
	if x != nil {
		return x.Url
	}
	return ""
}

func (x *NodeMeta) GetCommitIndex() uint64 {
	if x != nil {
		return x.CommitIndex
	}
	return 0
}

type Command struct {
	state         protoimpl.MessageState
	sizeCache     protoimpl.SizeCache
	unknownFields protoimpl.UnknownFields

	Type Command_Type `protobuf:"varint,1,opt,name=type,proto3,enum=cluster.Command_Type" json:"type,omitempty"`
	// Types that are assignable to Request:
	//
	//	*Command_ExecuteRequest
	//	*Command_QueryRequest
	//	*Command_BackupRequest
	//	*Command_LoadRequest
	//	*Command_RemoveNodeRequest
	//	*Command_NotifyRequest
	//	*Command_JoinRequest
	//	*Command_ExecuteQueryRequest
	//	*Command_LoadChunkRequest
	Request     isCommand_Request `protobuf_oneof:"request"`
	Credentials *Credentials      `protobuf:"bytes,4,opt,name=credentials,proto3" json:"credentials,omitempty"`
}

func (x *Command) Reset() {
	*x = Command{}
	mi := &file_message_proto_msgTypes[2]
	ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
	ms.StoreMessageInfo(mi)
}

func (x *Command) String() string {
	return protoimpl.X.MessageStringOf(x)
}

func (*Command) ProtoMessage() {}

func (x *Command) ProtoReflect() protoreflect.Message {
	mi := &file_message_proto_msgTypes[2]
	if x != nil {
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		if ms.LoadMessageInfo() == nil {
			ms.StoreMessageInfo(mi)
		}
		return ms
	}
	return mi.MessageOf(x)
}

// Deprecated: Use Command.ProtoReflect.Descriptor instead.
func (*Command) Descriptor() ([]byte, []int) {
	return file_message_proto_rawDescGZIP(), []int{2}
}

func (x *Command) GetType() Command_Type {
	if x != nil {
		return x.Type
	}
	return Command_COMMAND_TYPE_UNKNOWN
}

func (m *Command) GetRequest() isCommand_Request {
	if m != nil {
		return m.Request
	}
	return nil
}

func (x *Command) GetExecuteRequest() *proto.ExecuteRequest {
	if x, ok := x.GetRequest().(*Command_ExecuteRequest); ok {
		return x.ExecuteRequest
	}
	return nil
}

func (x *Command) GetQueryRequest() *proto.QueryRequest {
	if x, ok := x.GetRequest().(*Command_QueryRequest); ok {
		return x.QueryRequest
	}
	return nil
}

func (x *Command) GetBackupRequest() *proto.BackupRequest {
	if x, ok := x.GetRequest().(*Command_BackupRequest); ok {
		return x.BackupRequest
	}
	return nil
}

func (x *Command) GetLoadRequest() *proto.LoadRequest {
	if x, ok := x.GetRequest().(*Command_LoadRequest); ok {
		return x.LoadRequest
	}
	return nil
}

func (x *Command) GetRemoveNodeRequest() *proto.RemoveNodeRequest {
	if x, ok := x.GetRequest().(*Command_RemoveNodeRequest); ok {
		return x.RemoveNodeRequest
	}
	return nil
}

func (x *Command) GetNotifyRequest() *proto.NotifyRequest {
	if x, ok := x.GetRequest().(*Command_NotifyRequest); ok {
		return x.NotifyRequest
	}
	return nil
}

func (x *Command) GetJoinRequest() *proto.JoinRequest {
	if x, ok := x.GetRequest().(*Command_JoinRequest); ok {
		return x.JoinRequest
	}
	return nil
}

func (x *Command) GetExecuteQueryRequest() *proto.ExecuteQueryRequest {
	if x, ok := x.GetRequest().(*Command_ExecuteQueryRequest); ok {
		return x.ExecuteQueryRequest
	}
	return nil
}

func (x *Command) GetLoadChunkRequest() *proto.LoadChunkRequest {
	if x, ok := x.GetRequest().(*Command_LoadChunkRequest); ok {
		return x.LoadChunkRequest
	}
	return nil
}

func (x *Command) GetCredentials() *Credentials {
	if x != nil {
		return x.Credentials
	}
	return nil
}

type isCommand_Request interface {
	isCommand_Request()
}

type Command_ExecuteRequest struct {
	ExecuteRequest *proto.ExecuteRequest `protobuf:"bytes,2,opt,name=execute_request,json=executeRequest,proto3,oneof"`
}

type Command_QueryRequest struct {
	QueryRequest *proto.QueryRequest `protobuf:"bytes,3,opt,name=query_request,json=queryRequest,proto3,oneof"`
}

type Command_BackupRequest struct {
	BackupRequest *proto.BackupRequest `protobuf:"bytes,5,opt,name=backup_request,json=backupRequest,proto3,oneof"`
}

type Command_LoadRequest struct {
	LoadRequest *proto.LoadRequest `protobuf:"bytes,6,opt,name=load_request,json=loadRequest,proto3,oneof"`
}

type Command_RemoveNodeRequest struct {
	RemoveNodeRequest *proto.RemoveNodeRequest `protobuf:"bytes,7,opt,name=remove_node_request,json=removeNodeRequest,proto3,oneof"`
}

type Command_NotifyRequest struct {
	NotifyRequest *proto.NotifyRequest `protobuf:"bytes,8,opt,name=notify_request,json=notifyRequest,proto3,oneof"`
}

type Command_JoinRequest struct {
	JoinRequest *proto.JoinRequest `protobuf:"bytes,9,opt,name=join_request,json=joinRequest,proto3,oneof"`
}

type Command_ExecuteQueryRequest struct {
	ExecuteQueryRequest *proto.ExecuteQueryRequest `protobuf:"bytes,10,opt,name=execute_query_request,json=executeQueryRequest,proto3,oneof"`
}

type Command_LoadChunkRequest struct {
	LoadChunkRequest *proto.LoadChunkRequest `protobuf:"bytes,11,opt,name=load_chunk_request,json=loadChunkRequest,proto3,oneof"`
}

func (*Command_ExecuteRequest) isCommand_Request() {}

func (*Command_QueryRequest) isCommand_Request() {}

func (*Command_BackupRequest) isCommand_Request() {}

func (*Command_LoadRequest) isCommand_Request() {}

func (*Command_RemoveNodeRequest) isCommand_Request() {}

func (*Command_NotifyRequest) isCommand_Request() {}

func (*Command_JoinRequest) isCommand_Request() {}

func (*Command_ExecuteQueryRequest) isCommand_Request() {}

func (*Command_LoadChunkRequest) isCommand_Request() {}

type CommandExecuteResponse struct {
	state         protoimpl.MessageState
	sizeCache     protoimpl.SizeCache
	unknownFields protoimpl.UnknownFields

	Error    string                        `protobuf:"bytes,1,opt,name=error,proto3" json:"error,omitempty"`
	Response []*proto.ExecuteQueryResponse `protobuf:"bytes,2,rep,name=response,proto3" json:"response,omitempty"`
}

func (x *CommandExecuteResponse) Reset() {
	*x = CommandExecuteResponse{}
	mi := &file_message_proto_msgTypes[3]
	ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
	ms.StoreMessageInfo(mi)
}

func (x *CommandExecuteResponse) String() string {
	return protoimpl.X.MessageStringOf(x)
}

func (*CommandExecuteResponse) ProtoMessage() {}

func (x *CommandExecuteResponse) ProtoReflect() protoreflect.Message {
	mi := &file_message_proto_msgTypes[3]
	if x != nil {
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		if ms.LoadMessageInfo() == nil {
			ms.StoreMessageInfo(mi)
		}
		return ms
	}
	return mi.MessageOf(x)
}

// Deprecated: Use CommandExecuteResponse.ProtoReflect.Descriptor instead.
func (*CommandExecuteResponse) Descriptor() ([]byte, []int) {
	return file_message_proto_rawDescGZIP(), []int{3}
}

func (x *CommandExecuteResponse) GetError() string {
	if x != nil {
		return x.Error
	}
	return ""
}

func (x *CommandExecuteResponse) GetResponse() []*proto.ExecuteQueryResponse {
	if x != nil {
		return x.Response
	}
	return nil
}

type CommandQueryResponse struct {
	state         protoimpl.MessageState
	sizeCache     protoimpl.SizeCache
	unknownFields protoimpl.UnknownFields

	Error string             `protobuf:"bytes,1,opt,name=error,proto3" json:"error,omitempty"`
	Rows  []*proto.QueryRows `protobuf:"bytes,2,rep,name=rows,proto3" json:"rows,omitempty"`
}

func (x *CommandQueryResponse) Reset() {
	*x = CommandQueryResponse{}
	mi := &file_message_proto_msgTypes[4]
	ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
	ms.StoreMessageInfo(mi)
}

func (x *CommandQueryResponse) String() string {
	return protoimpl.X.MessageStringOf(x)
}

func (*CommandQueryResponse) ProtoMessage() {}

func (x *CommandQueryResponse) ProtoReflect() protoreflect.Message {
	mi := &file_message_proto_msgTypes[4]
	if x != nil {
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		if ms.LoadMessageInfo() == nil {
			ms.StoreMessageInfo(mi)
		}
		return ms
	}
	return mi.MessageOf(x)
}

// Deprecated: Use CommandQueryResponse.ProtoReflect.Descriptor instead.
func (*CommandQueryResponse) Descriptor() ([]byte, []int) {
	return file_message_proto_rawDescGZIP(), []int{4}
}

func (x *CommandQueryResponse) GetError() string {
	if x != nil {
		return x.Error
	}
	return ""
}

func (x *CommandQueryResponse) GetRows() []*proto.QueryRows {
	if x != nil {
		return x.Rows
	}
	return nil
}

type CommandRequestResponse struct {
	state         protoimpl.MessageState
	sizeCache     protoimpl.SizeCache
	unknownFields protoimpl.UnknownFields

	Error    string                        `protobuf:"bytes,1,opt,name=error,proto3" json:"error,omitempty"`
	Response []*proto.ExecuteQueryResponse `protobuf:"bytes,2,rep,name=response,proto3" json:"response,omitempty"`
}

func (x *CommandRequestResponse) Reset() {
	*x = CommandRequestResponse{}
	mi := &file_message_proto_msgTypes[5]
	ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
	ms.StoreMessageInfo(mi)
}

func (x *CommandRequestResponse) String() string {
	return protoimpl.X.MessageStringOf(x)
}

func (*CommandRequestResponse) ProtoMessage() {}

func (x *CommandRequestResponse) ProtoReflect() protoreflect.Message {
	mi := &file_message_proto_msgTypes[5]
	if x != nil {
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		if ms.LoadMessageInfo() == nil {
			ms.StoreMessageInfo(mi)
		}
		return ms
	}
	return mi.MessageOf(x)
}

// Deprecated: Use CommandRequestResponse.ProtoReflect.Descriptor instead.
func (*CommandRequestResponse) Descriptor() ([]byte, []int) {
	return file_message_proto_rawDescGZIP(), []int{5}
}

func (x *CommandRequestResponse) GetError() string {
	if x != nil {
		return x.Error
	}
	return ""
}

func (x *CommandRequestResponse) GetResponse() []*proto.ExecuteQueryResponse {
	if x != nil {
		return x.Response
	}
	return nil
}

type CommandBackupResponse struct {
	state         protoimpl.MessageState
	sizeCache     protoimpl.SizeCache
	unknownFields protoimpl.UnknownFields

	Error string `protobuf:"bytes,1,opt,name=error,proto3" json:"error,omitempty"`
	Data  []byte `protobuf:"bytes,2,opt,name=data,proto3" json:"data,omitempty"`
}

func (x *CommandBackupResponse) Reset() {
	*x = CommandBackupResponse{}
	mi := &file_message_proto_msgTypes[6]
	ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
	ms.StoreMessageInfo(mi)
}

func (x *CommandBackupResponse) String() string {
	return protoimpl.X.MessageStringOf(x)
}

func (*CommandBackupResponse) ProtoMessage() {}

func (x *CommandBackupResponse) ProtoReflect() protoreflect.Message {
	mi := &file_message_proto_msgTypes[6]
	if x != nil {
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		if ms.LoadMessageInfo() == nil {
			ms.StoreMessageInfo(mi)
		}
		return ms
	}
	return mi.MessageOf(x)
}

// Deprecated: Use CommandBackupResponse.ProtoReflect.Descriptor instead.
func (*CommandBackupResponse) Descriptor() ([]byte, []int) {
	return file_message_proto_rawDescGZIP(), []int{6}
}

func (x *CommandBackupResponse) GetError() string {
	if x != nil {
		return x.Error
	}
	return ""
}

func (x *CommandBackupResponse) GetData() []byte {
	if x != nil {
		return x.Data
	}
	return nil
}

type CommandLoadResponse struct {
	state         protoimpl.MessageState
	sizeCache     protoimpl.SizeCache
	unknownFields protoimpl.UnknownFields

	Error string `protobuf:"bytes,1,opt,name=error,proto3" json:"error,omitempty"`
}

func (x *CommandLoadResponse) Reset() {
	*x = CommandLoadResponse{}
	mi := &file_message_proto_msgTypes[7]
	ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
	ms.StoreMessageInfo(mi)
}

func (x *CommandLoadResponse) String() string {
	return protoimpl.X.MessageStringOf(x)
}

func (*CommandLoadResponse) ProtoMessage() {}

func (x *CommandLoadResponse) ProtoReflect() protoreflect.Message {
	mi := &file_message_proto_msgTypes[7]
	if x != nil {
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		if ms.LoadMessageInfo() == nil {
			ms.StoreMessageInfo(mi)
		}
		return ms
	}
	return mi.MessageOf(x)
}

// Deprecated: Use CommandLoadResponse.ProtoReflect.Descriptor instead.
func (*CommandLoadResponse) Descriptor() ([]byte, []int) {
	return file_message_proto_rawDescGZIP(), []int{7}
}

func (x *CommandLoadResponse) GetError() string {
	if x != nil {
		return x.Error
	}
	return ""
}

type CommandLoadChunkResponse struct {
	state         protoimpl.MessageState
	sizeCache     protoimpl.SizeCache
	unknownFields protoimpl.UnknownFields

	Error string `protobuf:"bytes,1,opt,name=error,proto3" json:"error,omitempty"`
}

func (x *CommandLoadChunkResponse) Reset() {
	*x = CommandLoadChunkResponse{}
	mi := &file_message_proto_msgTypes[8]
	ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
	ms.StoreMessageInfo(mi)
}

func (x *CommandLoadChunkResponse) String() string {
	return protoimpl.X.MessageStringOf(x)
}

func (*CommandLoadChunkResponse) ProtoMessage() {}

func (x *CommandLoadChunkResponse) ProtoReflect() protoreflect.Message {
	mi := &file_message_proto_msgTypes[8]
	if x != nil {
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		if ms.LoadMessageInfo() == nil {
			ms.StoreMessageInfo(mi)
		}
		return ms
	}
	return mi.MessageOf(x)
}

// Deprecated: Use CommandLoadChunkResponse.ProtoReflect.Descriptor instead.
func (*CommandLoadChunkResponse) Descriptor() ([]byte, []int) {
	return file_message_proto_rawDescGZIP(), []int{8}
}

func (x *CommandLoadChunkResponse) GetError() string {
	if x != nil {
		return x.Error
	}
	return ""
}

type CommandRemoveNodeResponse struct {
	state         protoimpl.MessageState
	sizeCache     protoimpl.SizeCache
	unknownFields protoimpl.UnknownFields

	Error string `protobuf:"bytes,1,opt,name=error,proto3" json:"error,omitempty"`
}

func (x *CommandRemoveNodeResponse) Reset() {
	*x = CommandRemoveNodeResponse{}
	mi := &file_message_proto_msgTypes[9]
	ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
	ms.StoreMessageInfo(mi)
}

func (x *CommandRemoveNodeResponse) String() string {
	return protoimpl.X.MessageStringOf(x)
}

func (*CommandRemoveNodeResponse) ProtoMessage() {}

func (x *CommandRemoveNodeResponse) ProtoReflect() protoreflect.Message {
	mi := &file_message_proto_msgTypes[9]
	if x != nil {
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		if ms.LoadMessageInfo() == nil {
			ms.StoreMessageInfo(mi)
		}
		return ms
	}
	return mi.MessageOf(x)
}

// Deprecated: Use CommandRemoveNodeResponse.ProtoReflect.Descriptor instead.
func (*CommandRemoveNodeResponse) Descriptor() ([]byte, []int) {
	return file_message_proto_rawDescGZIP(), []int{9}
}

func (x *CommandRemoveNodeResponse) GetError() string {
	if x != nil {
		return x.Error
	}
	return ""
}

type CommandNotifyResponse struct {
	state         protoimpl.MessageState
	sizeCache     protoimpl.SizeCache
	unknownFields protoimpl.UnknownFields

	Error string `protobuf:"bytes,1,opt,name=error,proto3" json:"error,omitempty"`
}

func (x *CommandNotifyResponse) Reset() {
	*x = CommandNotifyResponse{}
	mi := &file_message_proto_msgTypes[10]
	ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
	ms.StoreMessageInfo(mi)
}

func (x *CommandNotifyResponse) String() string {
	return protoimpl.X.MessageStringOf(x)
}

func (*CommandNotifyResponse) ProtoMessage() {}

func (x *CommandNotifyResponse) ProtoReflect() protoreflect.Message {
	mi := &file_message_proto_msgTypes[10]
	if x != nil {
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		if ms.LoadMessageInfo() == nil {
			ms.StoreMessageInfo(mi)
		}
		return ms
	}
	return mi.MessageOf(x)
}

// Deprecated: Use CommandNotifyResponse.ProtoReflect.Descriptor instead.
func (*CommandNotifyResponse) Descriptor() ([]byte, []int) {
	return file_message_proto_rawDescGZIP(), []int{10}
}

func (x *CommandNotifyResponse) GetError() string {
	if x != nil {
		return x.Error
	}
	return ""
}

type CommandJoinResponse struct {
	state         protoimpl.MessageState
	sizeCache     protoimpl.SizeCache
	unknownFields protoimpl.UnknownFields

	Error  string `protobuf:"bytes,1,opt,name=error,proto3" json:"error,omitempty"`
	Leader string `protobuf:"bytes,2,opt,name=leader,proto3" json:"leader,omitempty"`
}

func (x *CommandJoinResponse) Reset() {
	*x = CommandJoinResponse{}
	mi := &file_message_proto_msgTypes[11]
	ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
	ms.StoreMessageInfo(mi)
}

func (x *CommandJoinResponse) String() string {
	return protoimpl.X.MessageStringOf(x)
}

func (*CommandJoinResponse) ProtoMessage() {}

func (x *CommandJoinResponse) ProtoReflect() protoreflect.Message {
	mi := &file_message_proto_msgTypes[11]
	if x != nil {
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		if ms.LoadMessageInfo() == nil {
			ms.StoreMessageInfo(mi)
		}
		return ms
	}
	return mi.MessageOf(x)
}

// Deprecated: Use CommandJoinResponse.ProtoReflect.Descriptor instead.
func (*CommandJoinResponse) Descriptor() ([]byte, []int) {
	return file_message_proto_rawDescGZIP(), []int{11}
}

func (x *CommandJoinResponse) GetError() string {
	if x != nil {
		return x.Error
	}
	return ""
}

func (x *CommandJoinResponse) GetLeader() string {
	if x != nil {
		return x.Leader
	}
	return ""
}

var File_message_proto protoreflect.FileDescriptor

var file_message_proto_rawDesc = []byte{
	0x0a, 0x0d, 0x6d, 0x65, 0x73, 0x73, 0x61, 0x67, 0x65, 0x2e, 0x70, 0x72, 0x6f, 0x74, 0x6f, 0x12,
	0x07, 0x63, 0x6c, 0x75, 0x73, 0x74, 0x65, 0x72, 0x1a, 0x24, 0x69, 0x6e, 0x74, 0x65, 0x72, 0x6e,
	0x61, 0x6c, 0x2f, 0x63, 0x6f, 0x6d, 0x6d, 0x61, 0x6e, 0x64, 0x2f, 0x70, 0x72, 0x6f, 0x74, 0x6f,
	0x2f, 0x63, 0x6f, 0x6d, 0x6d, 0x61, 0x6e, 0x64, 0x2e, 0x70, 0x72, 0x6f, 0x74, 0x6f, 0x22, 0x45,
	0x0a, 0x0b, 0x43, 0x72, 0x65, 0x64, 0x65, 0x6e, 0x74, 0x69, 0x61, 0x6c, 0x73, 0x12, 0x1a, 0x0a,
	0x08, 0x75, 0x73, 0x65, 0x72, 0x6e, 0x61, 0x6d, 0x65, 0x18, 0x01, 0x20, 0x01, 0x28, 0x09, 0x52,
	0x08, 0x75, 0x73, 0x65, 0x72, 0x6e, 0x61, 0x6d, 0x65, 0x12, 0x1a, 0x0a, 0x08, 0x70, 0x61, 0x73,
	0x73, 0x77, 0x6f, 0x72, 0x64, 0x18, 0x02, 0x20, 0x01, 0x28, 0x09, 0x52, 0x08, 0x70, 0x61, 0x73,
	0x73, 0x77, 0x6f, 0x72, 0x64, 0x22, 0x3f, 0x0a, 0x08, 0x4e, 0x6f, 0x64, 0x65, 0x4d, 0x65, 0x74,
	0x61, 0x12, 0x10, 0x0a, 0x03, 0x75, 0x72, 0x6c, 0x18, 0x01, 0x20, 0x01, 0x28, 0x09, 0x52, 0x03,
	0x75, 0x72, 0x6c, 0x12, 0x21, 0x0a, 0x0c, 0x63, 0x6f, 0x6d, 0x6d, 0x69, 0x74, 0x5f, 0x69, 0x6e,
	0x64, 0x65, 0x78, 0x18, 0x02, 0x20, 0x01, 0x28, 0x04, 0x52, 0x0b, 0x63, 0x6f, 0x6d, 0x6d, 0x69,
	0x74, 0x49, 0x6e, 0x64, 0x65, 0x78, 0x22, 0xab, 0x08, 0x0a, 0x07, 0x43, 0x6f, 0x6d, 0x6d, 0x61,
	0x6e, 0x64, 0x12, 0x29, 0x0a, 0x04, 0x74, 0x79, 0x70, 0x65, 0x18, 0x01, 0x20, 0x01, 0x28, 0x0e,
	0x32, 0x15, 0x2e, 0x63, 0x6c, 0x75, 0x73, 0x74, 0x65, 0x72, 0x2e, 0x43, 0x6f, 0x6d, 0x6d, 0x61,
	0x6e, 0x64, 0x2e, 0x54, 0x79, 0x70, 0x65, 0x52, 0x04, 0x74, 0x79, 0x70, 0x65, 0x12, 0x42, 0x0a,
	0x0f, 0x65, 0x78, 0x65, 0x63, 0x75, 0x74, 0x65, 0x5f, 0x72, 0x65, 0x71, 0x75, 0x65, 0x73, 0x74,
	0x18, 0x02, 0x20, 0x01, 0x28, 0x0b, 0x32, 0x17, 0x2e, 0x63, 0x6f, 0x6d, 0x6d, 0x61, 0x6e, 0x64,
	0x2e, 0x45, 0x78, 0x65, 0x63, 0x75, 0x74, 0x65, 0x52, 0x65, 0x71, 0x75, 0x65, 0x73, 0x74, 0x48,
	0x00, 0x52, 0x0e, 0x65, 0x78, 0x65, 0x63, 0x75, 0x74, 0x65, 0x52, 0x65, 0x71, 0x75, 0x65, 0x73,
	0x74, 0x12, 0x3c, 0x0a, 0x0d, 0x71, 0x75, 0x65, 0x72, 0x79, 0x5f, 0x72, 0x65, 0x71, 0x75, 0x65,
	0x73, 0x74, 0x18, 0x03, 0x20, 0x01, 0x28, 0x0b, 0x32, 0x15, 0x2e, 0x63, 0x6f, 0x6d, 0x6d, 0x61,
	0x6e, 0x64, 0x2e, 0x51, 0x75, 0x65, 0x72, 0x79, 0x52, 0x65, 0x71, 0x75, 0x65, 0x73, 0x74, 0x48,
	0x00, 0x52, 0x0c, 0x71, 0x75, 0x65, 0x72, 0x79, 0x52, 0x65, 0x71, 0x75, 0x65, 0x73, 0x74, 0x12,
	0x3f, 0x0a, 0x0e, 0x62, 0x61, 0x63, 0x6b, 0x75, 0x70, 0x5f, 0x72, 0x65, 0x71, 0x75, 0x65, 0x73,
	0x74, 0x18, 0x05, 0x20, 0x01, 0x28, 0x0b, 0x32, 0x16, 0x2e, 0x63, 0x6f, 0x6d, 0x6d, 0x61, 0x6e,
	0x64, 0x2e, 0x42, 0x61, 0x63, 0x6b, 0x75, 0x70, 0x52, 0x65, 0x71, 0x75, 0x65, 0x73, 0x74, 0x48,
	0x00, 0x52, 0x0d, 0x62, 0x61, 0x63, 0x6b, 0x75, 0x70, 0x52, 0x65, 0x71, 0x75, 0x65, 0x73, 0x74,
	0x12, 0x39, 0x0a, 0x0c, 0x6c, 0x6f, 0x61, 0x64, 0x5f, 0x72, 0x65, 0x71, 0x75, 0x65, 0x73, 0x74,
	0x18, 0x06, 0x20, 0x01, 0x28, 0x0b, 0x32, 0x14, 0x2e, 0x63, 0x6f, 0x6d, 0x6d, 0x61, 0x6e, 0x64,
	0x2e, 0x4c, 0x6f, 0x61, 0x64, 0x52, 0x65, 0x71, 0x75, 0x65, 0x73, 0x74, 0x48, 0x00, 0x52, 0x0b,
	0x6c, 0x6f, 0x61, 0x64, 0x52, 0x65, 0x71, 0x75, 0x65, 0x73, 0x74, 0x12, 0x4c, 0x0a, 0x13, 0x72,
	0x65, 0x6d, 0x6f, 0x76, 0x65, 0x5f, 0x6e, 0x6f, 0x64, 0x65, 0x5f, 0x72, 0x65, 0x71, 0x75, 0x65,
	0x73, 0x74, 0x18, 0x07, 0x20, 0x01, 0x28, 0x0b, 0x32, 0x1a, 0x2e, 0x63, 0x6f, 0x6d, 0x6d, 0x61,
	0x6e, 0x64, 0x2e, 0x52, 0x65, 0x6d, 0x6f, 0x76, 0x65, 0x4e, 0x6f, 0x64, 0x65, 0x52, 0x65, 0x71,
	0x75, 0x65, 0x73, 0x74, 0x48, 0x00, 0x52, 0x11, 0x72, 0x65, 0x6d, 0x6f, 0x76, 0x65, 0x4e, 0x6f,
	0x64, 0x65, 0x52, 0x65, 0x71, 0x75, 0x65, 0x73, 0x74, 0x12, 0x3f, 0x0a, 0x0e, 0x6e, 0x6f, 0x74,
	0x69, 0x66, 0x79, 0x5f, 0x72, 0x65, 0x71, 0x75, 0x65, 0x73, 0x74, 0x18, 0x08, 0x20, 0x01, 0x28,
	0x0b, 0x32, 0x16, 0x2e, 0x63, 0x6f, 0x6d, 0x6d, 0x61, 0x6e, 0x64, 0x2e, 0x4e, 0x6f, 0x74, 0x69,
	0x66, 0x79, 0x52, 0x65, 0x71, 0x75, 0x65, 0x73, 0x74, 0x48, 0x00, 0x52, 0x0d, 0x6e, 0x6f, 0x74,
	0x69, 0x66, 0x79, 0x52, 0x65, 0x71, 0x75, 0x65, 0x73, 0x74, 0x12, 0x39, 0x0a, 0x0c, 0x6a, 0x6f,
	0x69, 0x6e, 0x5f, 0x72, 0x65, 0x71, 0x75, 0x65, 0x73, 0x74, 0x18, 0x09, 0x20, 0x01, 0x28, 0x0b,
	0x32, 0x14, 0x2e, 0x63, 0x6f, 0x6d, 0x6d, 0x61, 0x6e, 0x64, 0x2e, 0x4a, 0x6f, 0x69, 0x6e, 0x52,
	0x65, 0x71, 0x75, 0x65, 0x73, 0x74, 0x48, 0x00, 0x52, 0x0b, 0x6a, 0x6f, 0x69, 0x6e, 0x52, 0x65,
	0x71, 0x75, 0x65, 0x73, 0x74, 0x12, 0x52, 0x0a, 0x15, 0x65, 0x78, 0x65, 0x63, 0x75, 0x74, 0x65,
	0x5f, 0x71, 0x75, 0x65, 0x72, 0x79, 0x5f, 0x72, 0x65, 0x71, 0x75, 0x65, 0x73, 0x74, 0x18, 0x0a,
	0x20, 0x01, 0x28, 0x0b, 0x32, 0x1c, 0x2e, 0x63, 0x6f, 0x6d, 0x6d, 0x61, 0x6e, 0x64, 0x2e, 0x45,
	0x78, 0x65, 0x63, 0x75, 0x74, 0x65, 0x51, 0x75, 0x65, 0x72, 0x79, 0x52, 0x65, 0x71, 0x75, 0x65,
	0x73, 0x74, 0x48, 0x00, 0x52, 0x13, 0x65, 0x78, 0x65, 0x63, 0x75, 0x74, 0x65, 0x51, 0x75, 0x65,
	0x72, 0x79, 0x52, 0x65, 0x71, 0x75, 0x65, 0x73, 0x74, 0x12, 0x49, 0x0a, 0x12, 0x6c, 0x6f, 0x61,
	0x64, 0x5f, 0x63, 0x68, 0x75, 0x6e, 0x6b, 0x5f, 0x72, 0x65, 0x71, 0x75, 0x65, 0x73, 0x74, 0x18,
	0x0b, 0x20, 0x01, 0x28, 0x0b, 0x32, 0x19, 0x2e, 0x63, 0x6f, 0x6d, 0x6d, 0x61, 0x6e, 0x64, 0x2e,
	0x4c, 0x6f, 0x61, 0x64, 0x43, 0x68, 0x75, 0x6e, 0x6b, 0x52, 0x65, 0x71, 0x75, 0x65, 0x73, 0x74,
	0x48, 0x00, 0x52, 0x10, 0x6c, 0x6f, 0x61, 0x64, 0x43, 0x68, 0x75, 0x6e, 0x6b, 0x52, 0x65, 0x71,
	0x75, 0x65, 0x73, 0x74, 0x12, 0x36, 0x0a, 0x0b, 0x63, 0x72, 0x65, 0x64, 0x65, 0x6e, 0x74, 0x69,
	0x61, 0x6c, 0x73, 0x18, 0x04, 0x20, 0x01, 0x28, 0x0b, 0x32, 0x14, 0x2e, 0x63, 0x6c, 0x75, 0x73,
	0x74, 0x65, 0x72, 0x2e, 0x43, 0x72, 0x65, 0x64, 0x65, 0x6e, 0x74, 0x69, 0x61, 0x6c, 0x73, 0x52,
	0x0b, 0x63, 0x72, 0x65, 0x64, 0x65, 0x6e, 0x74, 0x69, 0x61, 0x6c, 0x73, 0x22, 0xca, 0x02, 0x0a,
	0x04, 0x54, 0x79, 0x70, 0x65, 0x12, 0x18, 0x0a, 0x14, 0x43, 0x4f, 0x4d, 0x4d, 0x41, 0x4e, 0x44,
	0x5f, 0x54, 0x59, 0x50, 0x45, 0x5f, 0x55, 0x4e, 0x4b, 0x4e, 0x4f, 0x57, 0x4e, 0x10, 0x00, 0x12,
	0x21, 0x0a, 0x1d, 0x43, 0x4f, 0x4d, 0x4d, 0x41, 0x4e, 0x44, 0x5f, 0x54, 0x59, 0x50, 0x45, 0x5f,
	0x47, 0x45, 0x54, 0x5f, 0x4e, 0x4f, 0x44, 0x45, 0x5f, 0x41, 0x50, 0x49, 0x5f, 0x55, 0x52, 0x4c,
	0x10, 0x01, 0x12, 0x18, 0x0a, 0x14, 0x43, 0x4f, 0x4d, 0x4d, 0x41, 0x4e, 0x44, 0x5f, 0x54, 0x59,
	0x50, 0x45, 0x5f, 0x45, 0x58, 0x45, 0x43, 0x55, 0x54, 0x45, 0x10, 0x02, 0x12, 0x16, 0x0a, 0x12,
	0x43, 0x4f, 0x4d, 0x4d, 0x41, 0x4e, 0x44, 0x5f, 0x54, 0x59, 0x50, 0x45, 0x5f, 0x51, 0x55, 0x45,
	0x52, 0x59, 0x10, 0x03, 0x12, 0x17, 0x0a, 0x13, 0x43, 0x4f, 0x4d, 0x4d, 0x41, 0x4e, 0x44, 0x5f,
	0x54, 0x59, 0x50, 0x45, 0x5f, 0x42, 0x41, 0x43, 0x4b, 0x55, 0x50, 0x10, 0x04, 0x12, 0x15, 0x0a,
	0x11, 0x43, 0x4f, 0x4d, 0x4d, 0x41, 0x4e, 0x44, 0x5f, 0x54, 0x59, 0x50, 0x45, 0x5f, 0x4c, 0x4f,
	0x41, 0x44, 0x10, 0x05, 0x12, 0x1c, 0x0a, 0x18, 0x43, 0x4f, 0x4d, 0x4d, 0x41, 0x4e, 0x44, 0x5f,
	0x54, 0x59, 0x50, 0x45, 0x5f, 0x52, 0x45, 0x4d, 0x4f, 0x56, 0x45, 0x5f, 0x4e, 0x4f, 0x44, 0x45,
	0x10, 0x06, 0x12, 0x17, 0x0a, 0x13, 0x43, 0x4f, 0x4d, 0x4d, 0x41, 0x4e, 0x44, 0x5f, 0x54, 0x59,
	0x50, 0x45, 0x5f, 0x4e, 0x4f, 0x54, 0x49, 0x46, 0x59, 0x10, 0x07, 0x12, 0x15, 0x0a, 0x11, 0x43,
	0x4f, 0x4d, 0x4d, 0x41, 0x4e, 0x44, 0x5f, 0x54, 0x59, 0x50, 0x45, 0x5f, 0x4a, 0x4f, 0x49, 0x4e,
	0x10, 0x08, 0x12, 0x18, 0x0a, 0x14, 0x43, 0x4f, 0x4d, 0x4d, 0x41, 0x4e, 0x44, 0x5f, 0x54, 0x59,
	0x50, 0x45, 0x5f, 0x52, 0x45, 0x51, 0x55, 0x45, 0x53, 0x54, 0x10, 0x09, 0x12, 0x1b, 0x0a, 0x17,
	0x43, 0x4f, 0x4d, 0x4d, 0x41, 0x4e, 0x44, 0x5f, 0x54, 0x59, 0x50, 0x45, 0x5f, 0x4c, 0x4f, 0x41,
	0x44, 0x5f, 0x43, 0x48, 0x55, 0x4e, 0x4b, 0x10, 0x0a, 0x12, 0x1e, 0x0a, 0x1a, 0x43, 0x4f, 0x4d,
	0x4d, 0x41, 0x4e, 0x44, 0x5f, 0x54, 0x59, 0x50, 0x45, 0x5f, 0x42, 0x41, 0x43, 0x4b, 0x55, 0x50,
	0x5f, 0x53, 0x54, 0x52, 0x45, 0x41, 0x4d, 0x10, 0x0b, 0x42, 0x09, 0x0a, 0x07, 0x72, 0x65, 0x71,
	0x75, 0x65, 0x73, 0x74, 0x22, 0x69, 0x0a, 0x16, 0x43, 0x6f, 0x6d, 0x6d, 0x61, 0x6e, 0x64, 0x45,
	0x78, 0x65, 0x63, 0x75, 0x74, 0x65, 0x52, 0x65, 0x73, 0x70, 0x6f, 0x6e, 0x73, 0x65, 0x12, 0x14,
	0x0a, 0x05, 0x65, 0x72, 0x72, 0x6f, 0x72, 0x18, 0x01, 0x20, 0x01, 0x28, 0x09, 0x52, 0x05, 0x65,
	0x72, 0x72, 0x6f, 0x72, 0x12, 0x39, 0x0a, 0x08, 0x72, 0x65, 0x73, 0x70, 0x6f, 0x6e, 0x73, 0x65,
	0x18, 0x02, 0x20, 0x03, 0x28, 0x0b, 0x32, 0x1d, 0x2e, 0x63, 0x6f, 0x6d, 0x6d, 0x61, 0x6e, 0x64,
	0x2e, 0x45, 0x78, 0x65, 0x63, 0x75, 0x74, 0x65, 0x51, 0x75, 0x65, 0x72, 0x79, 0x52, 0x65, 0x73,
	0x70, 0x6f, 0x6e, 0x73, 0x65, 0x52, 0x08, 0x72, 0x65, 0x73, 0x70, 0x6f, 0x6e, 0x73, 0x65, 0x22,
	0x54, 0x0a, 0x14, 0x43, 0x6f, 0x6d, 0x6d, 0x61, 0x6e, 0x64, 0x51, 0x75, 0x65, 0x72, 0x79, 0x52,
	0x65, 0x73, 0x70, 0x6f, 0x6e, 0x73, 0x65, 0x12, 0x14, 0x0a, 0x05, 0x65, 0x72, 0x72, 0x6f, 0x72,
	0x18, 0x01, 0x20, 0x01, 0x28, 0x09, 0x52, 0x05, 0x65, 0x72, 0x72, 0x6f, 0x72, 0x12, 0x26, 0x0a,
	0x04, 0x72, 0x6f, 0x77, 0x73, 0x18, 0x02, 0x20, 0x03, 0x28, 0x0b, 0x32, 0x12, 0x2e, 0x63, 0x6f,
	0x6d, 0x6d, 0x61, 0x6e, 0x64, 0x2e, 0x51, 0x75, 0x65, 0x72, 0x79, 0x52, 0x6f, 0x77, 0x73, 0x52,
	0x04, 0x72, 0x6f, 0x77, 0x73, 0x22, 0x69, 0x0a, 0x16, 0x43, 0x6f, 0x6d, 0x6d, 0x61, 0x6e, 0x64,
	0x52, 0x65, 0x71, 0x75, 0x65, 0x73, 0x74, 0x52, 0x65, 0x73, 0x70, 0x6f, 0x6e, 0x73, 0x65, 0x12,
	0x14, 0x0a, 0x05, 0x65, 0x72, 0x72, 0x6f, 0x72, 0x18, 0x01, 0x20, 0x01, 0x28, 0x09, 0x52, 0x05,
	0x65, 0x72, 0x72, 0x6f, 0x72, 0x12, 0x39, 0x0a, 0x08, 0x72, 0x65, 0x73, 0x70, 0x6f, 0x6e, 0x73,
	0x65, 0x18, 0x02, 0x20, 0x03, 0x28, 0x0b, 0x32, 0x1d, 0x2e, 0x63, 0x6f, 0x6d, 0x6d, 0x61, 0x6e,
	0x64, 0x2e, 0x45, 0x78, 0x65, 0x63, 0x75, 0x74, 0x65, 0x51, 0x75, 0x65, 0x72, 0x79, 0x52, 0x65,
	0x73, 0x70, 0x6f, 0x6e, 0x73, 0x65, 0x52, 0x08, 0x72, 0x65, 0x73, 0x70, 0x6f, 0x6e, 0x73, 0x65,
	0x22, 0x41, 0x0a, 0x15, 0x43, 0x6f, 0x6d, 0x6d, 0x61, 0x6e, 0x64, 0x42, 0x61, 0x63, 0x6b, 0x75,
	0x70, 0x52, 0x65, 0x73, 0x70, 0x6f, 0x6e, 0x73, 0x65, 0x12, 0x14, 0x0a, 0x05, 0x65, 0x72, 0x72,
	0x6f, 0x72, 0x18, 0x01, 0x20, 0x01, 0x28, 0x09, 0x52, 0x05, 0x65, 0x72, 0x72, 0x6f, 0x72, 0x12,
	0x12, 0x0a, 0x04, 0x64, 0x61, 0x74, 0x61, 0x18, 0x02, 0x20, 0x01, 0x28, 0x0c, 0x52, 0x04, 0x64,
	0x61, 0x74, 0x61, 0x22, 0x2b, 0x0a, 0x13, 0x43, 0x6f, 0x6d, 0x6d, 0x61, 0x6e, 0x64, 0x4c, 0x6f,
	0x61, 0x64, 0x52, 0x65, 0x73, 0x70, 0x6f, 0x6e, 0x73, 0x65, 0x12, 0x14, 0x0a, 0x05, 0x65, 0x72,
	0x72, 0x6f, 0x72, 0x18, 0x01, 0x20, 0x01, 0x28, 0x09, 0x52, 0x05, 0x65, 0x72, 0x72, 0x6f, 0x72,
	0x22, 0x30, 0x0a, 0x18, 0x43, 0x6f, 0x6d, 0x6d, 0x61, 0x6e, 0x64, 0x4c, 0x6f, 0x61, 0x64, 0x43,
	0x68, 0x75, 0x6e, 0x6b, 0x52, 0x65, 0x73, 0x70, 0x6f, 0x6e, 0x73, 0x65, 0x12, 0x14, 0x0a, 0x05,
	0x65, 0x72, 0x72, 0x6f, 0x72, 0x18, 0x01, 0x20, 0x01, 0x28, 0x09, 0x52, 0x05, 0x65, 0x72, 0x72,
	0x6f, 0x72, 0x22, 0x31, 0x0a, 0x19, 0x43, 0x6f, 0x6d, 0x6d, 0x61, 0x6e, 0x64, 0x52, 0x65, 0x6d,
	0x6f, 0x76, 0x65, 0x4e, 0x6f, 0x64, 0x65, 0x52, 0x65, 0x73, 0x70, 0x6f, 0x6e, 0x73, 0x65, 0x12,
	0x14, 0x0a, 0x05, 0x65, 0x72, 0x72, 0x6f, 0x72, 0x18, 0x01, 0x20, 0x01, 0x28, 0x09, 0x52, 0x05,
	0x65, 0x72, 0x72, 0x6f, 0x72, 0x22, 0x2d, 0x0a, 0x15, 0x43, 0x6f, 0x6d, 0x6d, 0x61, 0x6e, 0x64,
	0x4e, 0x6f, 0x74, 0x69, 0x66, 0x79, 0x52, 0x65, 0x73, 0x70, 0x6f, 0x6e, 0x73, 0x65, 0x12, 0x14,
	0x0a, 0x05, 0x65, 0x72, 0x72, 0x6f, 0x72, 0x18, 0x01, 0x20, 0x01, 0x28, 0x09, 0x52, 0x05, 0x65,
	0x72, 0x72, 0x6f, 0x72, 0x22, 0x43, 0x0a, 0x13, 0x43, 0x6f, 0x6d, 0x6d, 0x61, 0x6e, 0x64, 0x4a,
	0x6f, 0x69, 0x6e, 0x52, 0x65, 0x73, 0x70, 0x6f, 0x6e, 0x73, 0x65, 0x12, 0x14, 0x0a, 0x05, 0x65,
	0x72, 0x72, 0x6f, 0x72, 0x18, 0x01, 0x20, 0x01, 0x28, 0x09, 0x52, 0x05, 0x65, 0x72, 0x72, 0x6f,
	0x72, 0x12, 0x16, 0x0a, 0x06, 0x6c, 0x65, 0x61, 0x64, 0x65, 0x72, 0x18, 0x02, 0x20, 0x01, 0x28,
	0x09, 0x52, 0x06, 0x6c, 0x65, 0x61, 0x64, 0x65, 0x72, 0x42, 0x28, 0x5a, 0x26, 0x67, 0x69, 0x74,
	0x68, 0x75, 0x62, 0x2e, 0x63, 0x6f, 0x6d, 0x2f, 0x74, 0x61, 0x72, 0x75, 0x6e, 0x67, 0x6b, 0x61,
	0x2f, 0x77, 0x69, 0x72, 0x65, 0x2f, 0x63, 0x6c, 0x75, 0x73, 0x74, 0x65, 0x72, 0x2f, 0x70, 0x72,
	0x6f, 0x74, 0x6f, 0x62, 0x06, 0x70, 0x72, 0x6f, 0x74, 0x6f, 0x33,
}

var (
	file_message_proto_rawDescOnce sync.Once
	file_message_proto_rawDescData = file_message_proto_rawDesc
)

func file_message_proto_rawDescGZIP() []byte {
	file_message_proto_rawDescOnce.Do(func() {
		file_message_proto_rawDescData = protoimpl.X.CompressGZIP(file_message_proto_rawDescData)
	})
	return file_message_proto_rawDescData
}

var file_message_proto_enumTypes = make([]protoimpl.EnumInfo, 1)
var file_message_proto_msgTypes = make([]protoimpl.MessageInfo, 12)
var file_message_proto_goTypes = []any{
	(Command_Type)(0),                  // 0: cluster.Command.Type
	(*Credentials)(nil),                // 1: cluster.Credentials
	(*NodeMeta)(nil),                   // 2: cluster.NodeMeta
	(*Command)(nil),                    // 3: cluster.Command
	(*CommandExecuteResponse)(nil),     // 4: cluster.CommandExecuteResponse
	(*CommandQueryResponse)(nil),       // 5: cluster.CommandQueryResponse
	(*CommandRequestResponse)(nil),     // 6: cluster.CommandRequestResponse
	(*CommandBackupResponse)(nil),      // 7: cluster.CommandBackupResponse
	(*CommandLoadResponse)(nil),        // 8: cluster.CommandLoadResponse
	(*CommandLoadChunkResponse)(nil),   // 9: cluster.CommandLoadChunkResponse
	(*CommandRemoveNodeResponse)(nil),  // 10: cluster.CommandRemoveNodeResponse
	(*CommandNotifyResponse)(nil),      // 11: cluster.CommandNotifyResponse
	(*CommandJoinResponse)(nil),        // 12: cluster.CommandJoinResponse
	(*proto.ExecuteRequest)(nil),       // 13: command.ExecuteRequest
	(*proto.QueryRequest)(nil),         // 14: command.QueryRequest
	(*proto.BackupRequest)(nil),        // 15: command.BackupRequest
	(*proto.LoadRequest)(nil),          // 16: command.LoadRequest
	(*proto.RemoveNodeRequest)(nil),    // 17: command.RemoveNodeRequest
	(*proto.NotifyRequest)(nil),        // 18: command.NotifyRequest
	(*proto.JoinRequest)(nil),          // 19: command.JoinRequest
	(*proto.ExecuteQueryRequest)(nil),  // 20: command.ExecuteQueryRequest
	(*proto.LoadChunkRequest)(nil),     // 21: command.LoadChunkRequest
	(*proto.ExecuteQueryResponse)(nil), // 22: command.ExecuteQueryResponse
	(*proto.QueryRows)(nil),            // 23: command.QueryRows
}
var file_message_proto_depIdxs = []int32{
	0,  // 0: cluster.Command.type:type_name -> cluster.Command.Type
	13, // 1: cluster.Command.execute_request:type_name -> command.ExecuteRequest
	14, // 2: cluster.Command.query_request:type_name -> command.QueryRequest
	15, // 3: cluster.Command.backup_request:type_name -> command.BackupRequest
	16, // 4: cluster.Command.load_request:type_name -> command.LoadRequest
	17, // 5: cluster.Command.remove_node_request:type_name -> command.RemoveNodeRequest
	18, // 6: cluster.Command.notify_request:type_name -> command.NotifyRequest
	19, // 7: cluster.Command.join_request:type_name -> command.JoinRequest
	20, // 8: cluster.Command.execute_query_request:type_name -> command.ExecuteQueryRequest
	21, // 9: cluster.Command.load_chunk_request:type_name -> command.LoadChunkRequest
	1,  // 10: cluster.Command.credentials:type_name -> cluster.Credentials
	22, // 11: cluster.CommandExecuteResponse.response:type_name -> command.ExecuteQueryResponse
	23, // 12: cluster.CommandQueryResponse.rows:type_name -> command.QueryRows
	22, // 13: cluster.CommandRequestResponse.response:type_name -> command.ExecuteQueryResponse
	14, // [14:14] is the sub-list for method output_type
	14, // [14:14] is the sub-list for method input_type
	14, // [14:14] is the sub-list for extension type_name
	14, // [14:14] is the sub-list for extension extendee
	0,  // [0:14] is the sub-list for field type_name
}

func init() { file_message_proto_init() }
func file_message_proto_init() {
	if File_message_proto != nil {
		return
	}
	file_message_proto_msgTypes[2].OneofWrappers = []any{
		(*Command_ExecuteRequest)(nil),
		(*Command_QueryRequest)(nil),
		(*Command_BackupRequest)(nil),
		(*Command_LoadRequest)(nil),
		(*Command_RemoveNodeRequest)(nil),
		(*Command_NotifyRequest)(nil),
		(*Command_JoinRequest)(nil),
		(*Command_ExecuteQueryRequest)(nil),
		(*Command_LoadChunkRequest)(nil),
	}
	type x struct{}
	out := protoimpl.TypeBuilder{
		File: protoimpl.DescBuilder{
			GoPackagePath: reflect.TypeOf(x{}).PkgPath(),
			RawDescriptor: file_message_proto_rawDesc,
			NumEnums:      1,
			NumMessages:   12,
			NumExtensions: 0,
			NumServices:   0,
		},
		GoTypes:           file_message_proto_goTypes,
		DependencyIndexes: file_message_proto_depIdxs,
		EnumInfos:         file_message_proto_enumTypes,
		MessageInfos:      file_message_proto_msgTypes,
	}.Build()
	File_message_proto = out.File
	file_message_proto_rawDesc = nil
	file_message_proto_goTypes = nil
	file_message_proto_depIdxs = nil
}
