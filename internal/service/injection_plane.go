package service

import "github.com/google/uuid"

type InjectionPlane interface {
	StartInjectedCall(origin string, callID uuid.UUID, sourceISSI, destinationGSI uint32) bool
	StartInjectedGroupTX(
		origin string,
		callID uuid.UUID,
		sourceISSI, destinationGSI uint32,
		priority uint8,
		access uint8,
		service uint16,
	) bool
	IdleInjectedCall(origin string, callID uuid.UUID, cause uint8)
	ReleaseInjectedCall(origin string, callID uuid.UUID, cause uint8)
	InjectedVoiceFrame(origin string, callID uuid.UUID, data []byte)
	InjectedPacketFrame(origin string, callID uuid.UUID, data []byte)
	GroupSubscriberCount(gssi uint32) int
}
