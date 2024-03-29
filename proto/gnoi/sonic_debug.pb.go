// Code generated by protoc-gen-go. DO NOT EDIT.
// versions:
// 	protoc-gen-go v1.26.0
// 	protoc        v3.6.1
// source: sonic_debug.proto

package gnoi_sonic

import (
	context "context"
	gnmi "github.com/openconfig/gnmi/proto/gnmi"
	grpc "google.golang.org/grpc"
	codes "google.golang.org/grpc/codes"
	status "google.golang.org/grpc/status"
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

// Request message for GetSubscribePreferences RPC
type SubscribePreferencesReq struct {
	state         protoimpl.MessageState
	sizeCache     protoimpl.SizeCache
	unknownFields protoimpl.UnknownFields

	// Retrieve subscribe preferences for these paths.
	Path []*gnmi.Path `protobuf:"bytes,1,rep,name=path,proto3" json:"path,omitempty"`
}

func (x *SubscribePreferencesReq) Reset() {
	*x = SubscribePreferencesReq{}
	if protoimpl.UnsafeEnabled {
		mi := &file_sonic_debug_proto_msgTypes[0]
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		ms.StoreMessageInfo(mi)
	}
}

func (x *SubscribePreferencesReq) String() string {
	return protoimpl.X.MessageStringOf(x)
}

func (*SubscribePreferencesReq) ProtoMessage() {}

func (x *SubscribePreferencesReq) ProtoReflect() protoreflect.Message {
	mi := &file_sonic_debug_proto_msgTypes[0]
	if protoimpl.UnsafeEnabled && x != nil {
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		if ms.LoadMessageInfo() == nil {
			ms.StoreMessageInfo(mi)
		}
		return ms
	}
	return mi.MessageOf(x)
}

// Deprecated: Use SubscribePreferencesReq.ProtoReflect.Descriptor instead.
func (*SubscribePreferencesReq) Descriptor() ([]byte, []int) {
	return file_sonic_debug_proto_rawDescGZIP(), []int{0}
}

func (x *SubscribePreferencesReq) GetPath() []*gnmi.Path {
	if x != nil {
		return x.Path
	}
	return nil
}

// SubscribePreference holds subscription capability information for a path.
type SubscribePreference struct {
	state         protoimpl.MessageState
	sizeCache     protoimpl.SizeCache
	unknownFields protoimpl.UnknownFields

	// Resource path, whose subscribe preferences are indicated here.
	Path *gnmi.Path `protobuf:"bytes,1,opt,name=path,proto3" json:"path,omitempty"`
	// Indicates if ON_CHANGE subscription will be accepted for this path.
	OnChangeSupported bool `protobuf:"varint,2,opt,name=on_change_supported,json=onChangeSupported,proto3" json:"on_change_supported,omitempty"`
	// Indicates how TARGET_DEFINED subscription will be handled for this path.
	// It is possible to have target_defined_mode=ON_CHANGE but on_change_supported=false
	// when this container/list has both on_change supported and unsupported subpaths.
	TargetDefinedMode gnmi.SubscriptionMode `protobuf:"varint,3,opt,name=target_defined_mode,json=targetDefinedMode,proto3,enum=gnmi.SubscriptionMode" json:"target_defined_mode,omitempty"`
	// Indicates if wildcard keys are supported for this path.
	WildcardSupported bool `protobuf:"varint,4,opt,name=wildcard_supported,json=wildcardSupported,proto3" json:"wildcard_supported,omitempty"`
	// Minimum SAMPLE interval supported for this path, in nanoseconds.
	MinSampleInterval uint64 `protobuf:"varint,5,opt,name=min_sample_interval,json=minSampleInterval,proto3" json:"min_sample_interval,omitempty"`
}

func (x *SubscribePreference) Reset() {
	*x = SubscribePreference{}
	if protoimpl.UnsafeEnabled {
		mi := &file_sonic_debug_proto_msgTypes[1]
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		ms.StoreMessageInfo(mi)
	}
}

func (x *SubscribePreference) String() string {
	return protoimpl.X.MessageStringOf(x)
}

func (*SubscribePreference) ProtoMessage() {}

func (x *SubscribePreference) ProtoReflect() protoreflect.Message {
	mi := &file_sonic_debug_proto_msgTypes[1]
	if protoimpl.UnsafeEnabled && x != nil {
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		if ms.LoadMessageInfo() == nil {
			ms.StoreMessageInfo(mi)
		}
		return ms
	}
	return mi.MessageOf(x)
}

// Deprecated: Use SubscribePreference.ProtoReflect.Descriptor instead.
func (*SubscribePreference) Descriptor() ([]byte, []int) {
	return file_sonic_debug_proto_rawDescGZIP(), []int{1}
}

func (x *SubscribePreference) GetPath() *gnmi.Path {
	if x != nil {
		return x.Path
	}
	return nil
}

func (x *SubscribePreference) GetOnChangeSupported() bool {
	if x != nil {
		return x.OnChangeSupported
	}
	return false
}

func (x *SubscribePreference) GetTargetDefinedMode() gnmi.SubscriptionMode {
	if x != nil {
		return x.TargetDefinedMode
	}
	return gnmi.SubscriptionMode_TARGET_DEFINED
}

func (x *SubscribePreference) GetWildcardSupported() bool {
	if x != nil {
		return x.WildcardSupported
	}
	return false
}

func (x *SubscribePreference) GetMinSampleInterval() uint64 {
	if x != nil {
		return x.MinSampleInterval
	}
	return 0
}

var File_sonic_debug_proto protoreflect.FileDescriptor

var file_sonic_debug_proto_rawDesc = []byte{
	0x0a, 0x11, 0x73, 0x6f, 0x6e, 0x69, 0x63, 0x5f, 0x64, 0x65, 0x62, 0x75, 0x67, 0x2e, 0x70, 0x72,
	0x6f, 0x74, 0x6f, 0x12, 0x0a, 0x67, 0x6e, 0x6f, 0x69, 0x2e, 0x73, 0x6f, 0x6e, 0x69, 0x63, 0x1a,
	0x30, 0x67, 0x69, 0x74, 0x68, 0x75, 0x62, 0x2e, 0x63, 0x6f, 0x6d, 0x2f, 0x6f, 0x70, 0x65, 0x6e,
	0x63, 0x6f, 0x6e, 0x66, 0x69, 0x67, 0x2f, 0x67, 0x6e, 0x6d, 0x69, 0x2f, 0x70, 0x72, 0x6f, 0x74,
	0x6f, 0x2f, 0x67, 0x6e, 0x6d, 0x69, 0x2f, 0x67, 0x6e, 0x6d, 0x69, 0x2e, 0x70, 0x72, 0x6f, 0x74,
	0x6f, 0x22, 0x39, 0x0a, 0x17, 0x53, 0x75, 0x62, 0x73, 0x63, 0x72, 0x69, 0x62, 0x65, 0x50, 0x72,
	0x65, 0x66, 0x65, 0x72, 0x65, 0x6e, 0x63, 0x65, 0x73, 0x52, 0x65, 0x71, 0x12, 0x1e, 0x0a, 0x04,
	0x70, 0x61, 0x74, 0x68, 0x18, 0x01, 0x20, 0x03, 0x28, 0x0b, 0x32, 0x0a, 0x2e, 0x67, 0x6e, 0x6d,
	0x69, 0x2e, 0x50, 0x61, 0x74, 0x68, 0x52, 0x04, 0x70, 0x61, 0x74, 0x68, 0x22, 0x8c, 0x02, 0x0a,
	0x13, 0x53, 0x75, 0x62, 0x73, 0x63, 0x72, 0x69, 0x62, 0x65, 0x50, 0x72, 0x65, 0x66, 0x65, 0x72,
	0x65, 0x6e, 0x63, 0x65, 0x12, 0x1e, 0x0a, 0x04, 0x70, 0x61, 0x74, 0x68, 0x18, 0x01, 0x20, 0x01,
	0x28, 0x0b, 0x32, 0x0a, 0x2e, 0x67, 0x6e, 0x6d, 0x69, 0x2e, 0x50, 0x61, 0x74, 0x68, 0x52, 0x04,
	0x70, 0x61, 0x74, 0x68, 0x12, 0x2e, 0x0a, 0x13, 0x6f, 0x6e, 0x5f, 0x63, 0x68, 0x61, 0x6e, 0x67,
	0x65, 0x5f, 0x73, 0x75, 0x70, 0x70, 0x6f, 0x72, 0x74, 0x65, 0x64, 0x18, 0x02, 0x20, 0x01, 0x28,
	0x08, 0x52, 0x11, 0x6f, 0x6e, 0x43, 0x68, 0x61, 0x6e, 0x67, 0x65, 0x53, 0x75, 0x70, 0x70, 0x6f,
	0x72, 0x74, 0x65, 0x64, 0x12, 0x46, 0x0a, 0x13, 0x74, 0x61, 0x72, 0x67, 0x65, 0x74, 0x5f, 0x64,
	0x65, 0x66, 0x69, 0x6e, 0x65, 0x64, 0x5f, 0x6d, 0x6f, 0x64, 0x65, 0x18, 0x03, 0x20, 0x01, 0x28,
	0x0e, 0x32, 0x16, 0x2e, 0x67, 0x6e, 0x6d, 0x69, 0x2e, 0x53, 0x75, 0x62, 0x73, 0x63, 0x72, 0x69,
	0x70, 0x74, 0x69, 0x6f, 0x6e, 0x4d, 0x6f, 0x64, 0x65, 0x52, 0x11, 0x74, 0x61, 0x72, 0x67, 0x65,
	0x74, 0x44, 0x65, 0x66, 0x69, 0x6e, 0x65, 0x64, 0x4d, 0x6f, 0x64, 0x65, 0x12, 0x2d, 0x0a, 0x12,
	0x77, 0x69, 0x6c, 0x64, 0x63, 0x61, 0x72, 0x64, 0x5f, 0x73, 0x75, 0x70, 0x70, 0x6f, 0x72, 0x74,
	0x65, 0x64, 0x18, 0x04, 0x20, 0x01, 0x28, 0x08, 0x52, 0x11, 0x77, 0x69, 0x6c, 0x64, 0x63, 0x61,
	0x72, 0x64, 0x53, 0x75, 0x70, 0x70, 0x6f, 0x72, 0x74, 0x65, 0x64, 0x12, 0x2e, 0x0a, 0x13, 0x6d,
	0x69, 0x6e, 0x5f, 0x73, 0x61, 0x6d, 0x70, 0x6c, 0x65, 0x5f, 0x69, 0x6e, 0x74, 0x65, 0x72, 0x76,
	0x61, 0x6c, 0x18, 0x05, 0x20, 0x01, 0x28, 0x04, 0x52, 0x11, 0x6d, 0x69, 0x6e, 0x53, 0x61, 0x6d,
	0x70, 0x6c, 0x65, 0x49, 0x6e, 0x74, 0x65, 0x72, 0x76, 0x61, 0x6c, 0x32, 0x6a, 0x0a, 0x05, 0x44,
	0x65, 0x62, 0x75, 0x67, 0x12, 0x61, 0x0a, 0x17, 0x47, 0x65, 0x74, 0x53, 0x75, 0x62, 0x73, 0x63,
	0x72, 0x69, 0x62, 0x65, 0x50, 0x72, 0x65, 0x66, 0x65, 0x72, 0x65, 0x6e, 0x63, 0x65, 0x73, 0x12,
	0x23, 0x2e, 0x67, 0x6e, 0x6f, 0x69, 0x2e, 0x73, 0x6f, 0x6e, 0x69, 0x63, 0x2e, 0x53, 0x75, 0x62,
	0x73, 0x63, 0x72, 0x69, 0x62, 0x65, 0x50, 0x72, 0x65, 0x66, 0x65, 0x72, 0x65, 0x6e, 0x63, 0x65,
	0x73, 0x52, 0x65, 0x71, 0x1a, 0x1f, 0x2e, 0x67, 0x6e, 0x6f, 0x69, 0x2e, 0x73, 0x6f, 0x6e, 0x69,
	0x63, 0x2e, 0x53, 0x75, 0x62, 0x73, 0x63, 0x72, 0x69, 0x62, 0x65, 0x50, 0x72, 0x65, 0x66, 0x65,
	0x72, 0x65, 0x6e, 0x63, 0x65, 0x30, 0x01, 0x42, 0x0f, 0x5a, 0x0d, 0x2e, 0x2f, 0x3b, 0x67, 0x6e,
	0x6f, 0x69, 0x5f, 0x73, 0x6f, 0x6e, 0x69, 0x63, 0x62, 0x06, 0x70, 0x72, 0x6f, 0x74, 0x6f, 0x33,
}

var (
	file_sonic_debug_proto_rawDescOnce sync.Once
	file_sonic_debug_proto_rawDescData = file_sonic_debug_proto_rawDesc
)

func file_sonic_debug_proto_rawDescGZIP() []byte {
	file_sonic_debug_proto_rawDescOnce.Do(func() {
		file_sonic_debug_proto_rawDescData = protoimpl.X.CompressGZIP(file_sonic_debug_proto_rawDescData)
	})
	return file_sonic_debug_proto_rawDescData
}

var file_sonic_debug_proto_msgTypes = make([]protoimpl.MessageInfo, 2)
var file_sonic_debug_proto_goTypes = []interface{}{
	(*SubscribePreferencesReq)(nil), // 0: gnoi.sonic.SubscribePreferencesReq
	(*SubscribePreference)(nil),     // 1: gnoi.sonic.SubscribePreference
	(*gnmi.Path)(nil),               // 2: gnmi.Path
	(gnmi.SubscriptionMode)(0),      // 3: gnmi.SubscriptionMode
}
var file_sonic_debug_proto_depIdxs = []int32{
	2, // 0: gnoi.sonic.SubscribePreferencesReq.path:type_name -> gnmi.Path
	2, // 1: gnoi.sonic.SubscribePreference.path:type_name -> gnmi.Path
	3, // 2: gnoi.sonic.SubscribePreference.target_defined_mode:type_name -> gnmi.SubscriptionMode
	0, // 3: gnoi.sonic.Debug.GetSubscribePreferences:input_type -> gnoi.sonic.SubscribePreferencesReq
	1, // 4: gnoi.sonic.Debug.GetSubscribePreferences:output_type -> gnoi.sonic.SubscribePreference
	4, // [4:5] is the sub-list for method output_type
	3, // [3:4] is the sub-list for method input_type
	3, // [3:3] is the sub-list for extension type_name
	3, // [3:3] is the sub-list for extension extendee
	0, // [0:3] is the sub-list for field type_name
}

func init() { file_sonic_debug_proto_init() }
func file_sonic_debug_proto_init() {
	if File_sonic_debug_proto != nil {
		return
	}
	if !protoimpl.UnsafeEnabled {
		file_sonic_debug_proto_msgTypes[0].Exporter = func(v interface{}, i int) interface{} {
			switch v := v.(*SubscribePreferencesReq); i {
			case 0:
				return &v.state
			case 1:
				return &v.sizeCache
			case 2:
				return &v.unknownFields
			default:
				return nil
			}
		}
		file_sonic_debug_proto_msgTypes[1].Exporter = func(v interface{}, i int) interface{} {
			switch v := v.(*SubscribePreference); i {
			case 0:
				return &v.state
			case 1:
				return &v.sizeCache
			case 2:
				return &v.unknownFields
			default:
				return nil
			}
		}
	}
	type x struct{}
	out := protoimpl.TypeBuilder{
		File: protoimpl.DescBuilder{
			GoPackagePath: reflect.TypeOf(x{}).PkgPath(),
			RawDescriptor: file_sonic_debug_proto_rawDesc,
			NumEnums:      0,
			NumMessages:   2,
			NumExtensions: 0,
			NumServices:   1,
		},
		GoTypes:           file_sonic_debug_proto_goTypes,
		DependencyIndexes: file_sonic_debug_proto_depIdxs,
		MessageInfos:      file_sonic_debug_proto_msgTypes,
	}.Build()
	File_sonic_debug_proto = out.File
	file_sonic_debug_proto_rawDesc = nil
	file_sonic_debug_proto_goTypes = nil
	file_sonic_debug_proto_depIdxs = nil
}

// Reference imports to suppress errors if they are not otherwise used.
var _ context.Context
var _ grpc.ClientConnInterface

// This is a compile-time assertion to ensure that this generated file
// is compatible with the grpc package it is being compiled against.
const _ = grpc.SupportPackageIsVersion6

// DebugClient is the client API for Debug service.
//
// For semantics around ctx use and closing/ending streaming RPCs, please refer to https://godoc.org/google.golang.org/grpc#ClientConn.NewStream.
type DebugClient interface {
	// GetSubscribePreferences returns the subscription capability info for specific
	// paths and their subpaths.
	GetSubscribePreferences(ctx context.Context, in *SubscribePreferencesReq, opts ...grpc.CallOption) (Debug_GetSubscribePreferencesClient, error)
}

type debugClient struct {
	cc grpc.ClientConnInterface
}

func NewDebugClient(cc grpc.ClientConnInterface) DebugClient {
	return &debugClient{cc}
}

func (c *debugClient) GetSubscribePreferences(ctx context.Context, in *SubscribePreferencesReq, opts ...grpc.CallOption) (Debug_GetSubscribePreferencesClient, error) {
	stream, err := c.cc.NewStream(ctx, &_Debug_serviceDesc.Streams[0], "/gnoi.sonic.Debug/GetSubscribePreferences", opts...)
	if err != nil {
		return nil, err
	}
	x := &debugGetSubscribePreferencesClient{stream}
	if err := x.ClientStream.SendMsg(in); err != nil {
		return nil, err
	}
	if err := x.ClientStream.CloseSend(); err != nil {
		return nil, err
	}
	return x, nil
}

type Debug_GetSubscribePreferencesClient interface {
	Recv() (*SubscribePreference, error)
	grpc.ClientStream
}

type debugGetSubscribePreferencesClient struct {
	grpc.ClientStream
}

func (x *debugGetSubscribePreferencesClient) Recv() (*SubscribePreference, error) {
	m := new(SubscribePreference)
	if err := x.ClientStream.RecvMsg(m); err != nil {
		return nil, err
	}
	return m, nil
}

// DebugServer is the server API for Debug service.
type DebugServer interface {
	// GetSubscribePreferences returns the subscription capability info for specific
	// paths and their subpaths.
	GetSubscribePreferences(*SubscribePreferencesReq, Debug_GetSubscribePreferencesServer) error
}

// UnimplementedDebugServer can be embedded to have forward compatible implementations.
type UnimplementedDebugServer struct {
}

func (*UnimplementedDebugServer) GetSubscribePreferences(*SubscribePreferencesReq, Debug_GetSubscribePreferencesServer) error {
	return status.Errorf(codes.Unimplemented, "method GetSubscribePreferences not implemented")
}

func RegisterDebugServer(s *grpc.Server, srv DebugServer) {
	s.RegisterService(&_Debug_serviceDesc, srv)
}

func _Debug_GetSubscribePreferences_Handler(srv interface{}, stream grpc.ServerStream) error {
	m := new(SubscribePreferencesReq)
	if err := stream.RecvMsg(m); err != nil {
		return err
	}
	return srv.(DebugServer).GetSubscribePreferences(m, &debugGetSubscribePreferencesServer{stream})
}

type Debug_GetSubscribePreferencesServer interface {
	Send(*SubscribePreference) error
	grpc.ServerStream
}

type debugGetSubscribePreferencesServer struct {
	grpc.ServerStream
}

func (x *debugGetSubscribePreferencesServer) Send(m *SubscribePreference) error {
	return x.ServerStream.SendMsg(m)
}

var _Debug_serviceDesc = grpc.ServiceDesc{
	ServiceName: "gnoi.sonic.Debug",
	HandlerType: (*DebugServer)(nil),
	Methods:     []grpc.MethodDesc{},
	Streams: []grpc.StreamDesc{
		{
			StreamName:    "GetSubscribePreferences",
			Handler:       _Debug_GetSubscribePreferences_Handler,
			ServerStreams: true,
		},
	},
	Metadata: "sonic_debug.proto",
}
