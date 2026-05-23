package federation

import (
	"encoding/hex"

	federationv2pb "github.com/freetetra/server/internal/federation/proto/v2"
	"google.golang.org/protobuf/types/known/structpb"
)

func messageToControl(msg *Message) *federationv2pb.Control {
	if msg == nil {
		return nil
	}
	ctrl := &federationv2pb.Control{
		Origin:          msg.Origin,
		ProtocolVersion: uint32(msg.Version),
		MsgId:           msg.MsgID,
		Ttl:             int32(msg.TTL),
		Path:            append([]string(nil), msg.Path...),
	}

	switch msg.Type {
	case MsgHello:
		ctrl.Payload = &federationv2pb.Control_Hello{Hello: &federationv2pb.Hello{
			UdpAddr:  msg.UDPAddr,
			UdpToken: msg.UDPToken,
		}}
	case MsgSubscriberUpdate:
		action := federationv2pb.SubscriberUpdate_ACTION_UNSPECIFIED
		if msg.Action == "register" {
			action = federationv2pb.SubscriberUpdate_ACTION_REGISTER
		}
		if msg.Action == "deregister" {
			action = federationv2pb.SubscriberUpdate_ACTION_DEREGISTER
		}
		ctrl.Payload = &federationv2pb.Control_SubscriberUpdate{
			SubscriberUpdate: &federationv2pb.SubscriberUpdate{
				Issi:   msg.ISSI,
				Action: action,
				Gssis:  append([]uint32(nil), msg.GSSIs...),
			},
		}
	case MsgAffiliateUpdate:
		action := federationv2pb.AffiliateUpdate_ACTION_UNSPECIFIED
		if msg.Action == "affiliate" {
			action = federationv2pb.AffiliateUpdate_ACTION_AFFILIATE
		}
		if msg.Action == "deaffiliate" {
			action = federationv2pb.AffiliateUpdate_ACTION_DEAFFILIATE
		}
		ctrl.Payload = &federationv2pb.Control_AffiliateUpdate{
			AffiliateUpdate: &federationv2pb.AffiliateUpdate{
				Issi:   msg.ISSI,
				Action: action,
				Gssis:  append([]uint32(nil), msg.GSSIs...),
			},
		}
	case MsgCallStart:
		ctrl.Payload = &federationv2pb.Control_CallStart{CallStart: &federationv2pb.CallStart{
			Uuid:       msg.UUID,
			SourceIssi: msg.SourceISSI,
			DestGssi:   msg.DestGSSI,
			Priority:   uint32(msg.Priority),
			Service:    uint32(msg.Service),
		}}
	case MsgCallEnd:
		ctrl.Payload = &federationv2pb.Control_CallEnd{CallEnd: &federationv2pb.CallEnd{
			Uuid:  msg.UUID,
			Cause: uint32(msg.Cause),
		}}
	case MsgSDSRelay:
		raw, _ := hex.DecodeString(msg.SDSData)
		ctrl.Payload = &federationv2pb.Control_SdsRelay{SdsRelay: &federationv2pb.SdsRelay{
			SourceIssi: msg.SourceISSI,
			DestIssi:   msg.DestISSI,
			SdsData:    raw,
		}}
	case MsgSyncRequest:
		ctrl.Payload = &federationv2pb.Control_SyncRequest{SyncRequest: &federationv2pb.SyncRequest{}}
	case MsgSyncResponse:
		subs := make(map[string]*federationv2pb.SyncSubscriber, len(msg.Subscribers))
		for issi, sub := range msg.Subscribers {
			subs[issi] = &federationv2pb.SyncSubscriber{
				Gssis:    append([]uint32(nil), sub.GSSIs...),
				Callsign: sub.Callsign,
			}
		}
		ctrl.Payload = &federationv2pb.Control_SyncResponse{
			SyncResponse: &federationv2pb.SyncResponse{Subscribers: subs},
		}
	case MsgPeerExchange:
		peers := make([]*federationv2pb.GossipPeer, 0, len(msg.Peers))
		for _, p := range msg.Peers {
			peers = append(peers, &federationv2pb.GossipPeer{Name: p.Name, Url: p.URL})
		}
		ctrl.Payload = &federationv2pb.Control_PeerExchange{
			PeerExchange: &federationv2pb.PeerExchange{Peers: peers},
		}
	case MsgUsersDBOffer:
		ctrl.Payload = &federationv2pb.Control_UsersDbOffer{UsersDbOffer: &federationv2pb.UsersDbOffer{
			Timestamp: msg.UsersDBTimestamp,
			Url:       msg.UsersDBURL,
			Count:     uint32(msg.UsersDBCount),
		}}
	case MsgUsersDBRequest:
		ctrl.Payload = &federationv2pb.Control_UsersDbRequest{UsersDbRequest: &federationv2pb.UsersDbRequest{}}
	case MsgPositionSample:
		ctrl.Payload = &federationv2pb.Control_PositionSample{PositionSample: &federationv2pb.PositionSample{
			Issi:     msg.ISSI,
			Lat:      msg.Lat,
			Lon:      msg.Lon,
			Repeater: msg.Repeater,
		}}
	case MsgStationUpdate:
		st, _ := structpb.NewStruct(msg.Station)
		if st == nil {
			st = &structpb.Struct{}
		}
		ctrl.Payload = &federationv2pb.Control_StationUpdate{
			StationUpdate: &federationv2pb.StationUpdate{Station: st},
		}
	}

	return ctrl
}

func controlToMessage(ctrl *federationv2pb.Control) *Message {
	if ctrl == nil {
		return nil
	}
	msg := &Message{
		Origin:  ctrl.GetOrigin(),
		Version: int(ctrl.GetProtocolVersion()),
		MsgID:   ctrl.GetMsgId(),
		TTL:     int(ctrl.GetTtl()),
		Path:    append([]string(nil), ctrl.GetPath()...),
	}

	switch p := ctrl.GetPayload().(type) {
	case *federationv2pb.Control_Hello:
		msg.Type = MsgHello
		msg.UDPAddr = p.Hello.GetUdpAddr()
		msg.UDPToken = p.Hello.GetUdpToken()
	case *federationv2pb.Control_SubscriberUpdate:
		msg.Type = MsgSubscriberUpdate
		msg.ISSI = p.SubscriberUpdate.GetIssi()
		msg.GSSIs = append([]uint32(nil), p.SubscriberUpdate.GetGssis()...)
		if p.SubscriberUpdate.GetAction() == federationv2pb.SubscriberUpdate_ACTION_REGISTER {
			msg.Action = "register"
		}
		if p.SubscriberUpdate.GetAction() == federationv2pb.SubscriberUpdate_ACTION_DEREGISTER {
			msg.Action = "deregister"
		}
	case *federationv2pb.Control_AffiliateUpdate:
		msg.Type = MsgAffiliateUpdate
		msg.ISSI = p.AffiliateUpdate.GetIssi()
		msg.GSSIs = append([]uint32(nil), p.AffiliateUpdate.GetGssis()...)
		if p.AffiliateUpdate.GetAction() == federationv2pb.AffiliateUpdate_ACTION_AFFILIATE {
			msg.Action = "affiliate"
		}
		if p.AffiliateUpdate.GetAction() == federationv2pb.AffiliateUpdate_ACTION_DEAFFILIATE {
			msg.Action = "deaffiliate"
		}
	case *federationv2pb.Control_CallStart:
		msg.Type = MsgCallStart
		msg.UUID = p.CallStart.GetUuid()
		msg.SourceISSI = p.CallStart.GetSourceIssi()
		msg.DestGSSI = p.CallStart.GetDestGssi()
		msg.Priority = uint8(p.CallStart.GetPriority())
		msg.Service = uint16(p.CallStart.GetService())
	case *federationv2pb.Control_CallEnd:
		msg.Type = MsgCallEnd
		msg.UUID = p.CallEnd.GetUuid()
		msg.Cause = uint8(p.CallEnd.GetCause())
	case *federationv2pb.Control_SdsRelay:
		msg.Type = MsgSDSRelay
		msg.SourceISSI = p.SdsRelay.GetSourceIssi()
		msg.DestISSI = p.SdsRelay.GetDestIssi()
		msg.SDSData = hex.EncodeToString(p.SdsRelay.GetSdsData())
	case *federationv2pb.Control_SyncRequest:
		msg.Type = MsgSyncRequest
	case *federationv2pb.Control_SyncResponse:
		msg.Type = MsgSyncResponse
		msg.Subscribers = make(map[string]SyncSubscriber, len(p.SyncResponse.GetSubscribers()))
		for issi, sub := range p.SyncResponse.GetSubscribers() {
			msg.Subscribers[issi] = SyncSubscriber{
				GSSIs:    append([]uint32(nil), sub.GetGssis()...),
				Callsign: sub.GetCallsign(),
			}
		}
	case *federationv2pb.Control_PeerExchange:
		msg.Type = MsgPeerExchange
		msg.Peers = make([]GossipPeer, 0, len(p.PeerExchange.GetPeers()))
		for _, gp := range p.PeerExchange.GetPeers() {
			msg.Peers = append(msg.Peers, GossipPeer{Name: gp.GetName(), URL: gp.GetUrl()})
		}
	case *federationv2pb.Control_UsersDbOffer:
		msg.Type = MsgUsersDBOffer
		msg.UsersDBTimestamp = p.UsersDbOffer.GetTimestamp()
		msg.UsersDBURL = p.UsersDbOffer.GetUrl()
		msg.UsersDBCount = int(p.UsersDbOffer.GetCount())
	case *federationv2pb.Control_UsersDbRequest:
		msg.Type = MsgUsersDBRequest
	case *federationv2pb.Control_PositionSample:
		msg.Type = MsgPositionSample
		msg.ISSI = p.PositionSample.GetIssi()
		msg.Lat = p.PositionSample.GetLat()
		msg.Lon = p.PositionSample.GetLon()
		msg.Repeater = p.PositionSample.GetRepeater()
	case *federationv2pb.Control_StationUpdate:
		msg.Type = MsgStationUpdate
		if p.StationUpdate.GetStation() != nil {
			msg.Station = p.StationUpdate.GetStation().AsMap()
		}
	}

	return msg
}
