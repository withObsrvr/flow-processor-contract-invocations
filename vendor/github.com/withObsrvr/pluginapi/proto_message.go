package pluginapi

import (
	"time"

	"github.com/withObsrvr/pluginapi/pb"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"
)

type ProtoMessage struct {
	msg *pb.PluginMessage
}

func NewProtoMessage(payload []byte) *ProtoMessage {
	return &ProtoMessage{
		msg: &pb.PluginMessage{
			Payload:   payload,
			Metadata:  make(map[string]*pb.Value),
			Timestamp: timestamppb.Now(),
		},
	}
}

func (m *ProtoMessage) SetMetadata(key string, value interface{}) error {
	pbValue, err := convertToPbValue(value)
	if err != nil {
		return err
	}
	m.msg.Metadata[key] = pbValue
	return nil
}

func (m *ProtoMessage) GetMetadata(key string) (interface{}, bool) {
	val, ok := m.msg.Metadata[key]
	if !ok {
		return nil, false
	}
	return convertFromPbValue(val), true
}

func (m *ProtoMessage) Payload() []byte {
	return m.msg.Payload
}

func (m *ProtoMessage) Timestamp() time.Time {
	return m.msg.Timestamp.AsTime()
}

func (m *ProtoMessage) Marshal() ([]byte, error) {
	return proto.Marshal(m.msg)
}

func UnmarshalProtoMessage(data []byte) (*ProtoMessage, error) {
	msg := &pb.PluginMessage{}
	if err := proto.Unmarshal(data, msg); err != nil {
		return nil, err
	}
	return &ProtoMessage{msg: msg}, nil
}
