package service

// registerLiveHandlers registers only the /api/live/last-heard JSON
// endpoint. The /live HTML page is served by the Vue SPA via
// registerSPAFallback at "/".
func (s *Service) registerLiveHandlers() {
	s.server.RegisterHTTPHandler("/api/live/last-heard", s.handleLastHeardAPI)
}
