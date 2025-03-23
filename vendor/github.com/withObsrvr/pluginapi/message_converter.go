package pluginapi

import (
	"fmt"

	"github.com/withObsrvr/pluginapi/pb"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func ProtoMessageToMessage(pm *ProtoMessage) *Message {
	metadata := make(map[string]interface{})
	for k, v := range pm.msg.Metadata {
		metadata[k] = convertFromPbValue(v)
	}

	return &Message{
		Payload:   pm.msg.Payload,
		Metadata:  metadata,
		Timestamp: pm.msg.Timestamp.AsTime(),
	}
}

func MessageToProtoMessage(m *Message) (*ProtoMessage, error) {
	metadata := make(map[string]*pb.Value)
	for k, v := range m.Metadata {
		pbValue, err := convertToPbValue(v)
		if err != nil {
			return nil, err
		}
		metadata[k] = pbValue
	}

	return &ProtoMessage{
		msg: &pb.PluginMessage{
			Payload:   m.Payload.([]byte),
			Metadata:  metadata,
			Timestamp: timestamppb.New(m.Timestamp),
		},
	}, nil
}

func convertFromPbValue(v *pb.Value) interface{} {
	switch x := v.Kind.(type) {
	case *pb.Value_StringValue:
		return x.StringValue
	case *pb.Value_IntValue:
		return x.IntValue
	case *pb.Value_FloatValue:
		return x.FloatValue
	case *pb.Value_BoolValue:
		return x.BoolValue
	case *pb.Value_BytesValue:
		return x.BytesValue
	default:
		return nil
	}
}

func convertToPbValue(v interface{}) (*pb.Value, error) {
	switch val := v.(type) {
	case string:
		return &pb.Value{Kind: &pb.Value_StringValue{StringValue: val}}, nil
	case int64:
		return &pb.Value{Kind: &pb.Value_IntValue{IntValue: val}}, nil
	case float64:
		return &pb.Value{Kind: &pb.Value_FloatValue{FloatValue: val}}, nil
	case bool:
		return &pb.Value{Kind: &pb.Value_BoolValue{BoolValue: val}}, nil
	case []byte:
		return &pb.Value{Kind: &pb.Value_BytesValue{BytesValue: val}}, nil
	default:
		return nil, fmt.Errorf("unsupported type: %T", v)
	}
}
