package coordinator

import "net/http"

// leaderOnly wraps a handler to ensure only the leader serves write requests.
// Standby nodes are redirected to the leader.
func (s *HTTPServer) leaderOnly(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if s.coord.IsReady() {
			next(w, r)
			return
		}
		info, _, _ := s.coord.GetLeaderInfo()
		s.writeStandbyRedirect(w, r, info)
	}
}
