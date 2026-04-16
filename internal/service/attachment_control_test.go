package service

import (
	"testing"

	"github.com/freetetra/server/internal/config"
)

func TestExcludeAttachmentSubscriber(t *testing.T) {
	s := &Service{
		cfg: config.Config{
			WebRadio: config.WebRadioConfig{BrewISSI: 900001},
			Zello:    config.ZelloConfig{BrewISSI: 899001},
		},
	}

	if !s.excludeAttachmentSubscriber(900001) {
		t.Fatalf("expected WEBRADIO_BREW_ISSI to be excluded")
	}
	if !s.excludeAttachmentSubscriber(899001) {
		t.Fatalf("expected ZELLO_BREW_ISSI to be excluded")
	}
	if s.excludeAttachmentSubscriber(501) {
		t.Fatalf("unexpected exclusion for normal subscriber")
	}
}
