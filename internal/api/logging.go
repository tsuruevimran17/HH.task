package api

import (
    "encoding/json"
    "time"
)

func (s *Server) logEvent(event string, fields map[string]any) {
    payload := map[string]any{
        "event": event,
        "ts":    time.Now().UTC().Format(time.RFC3339Nano),
    }
    for k, v := range fields {
        payload[k] = v
    }
    data, err := json.Marshal(payload)
    if err != nil {
        s.logger.Printf("log_marshal_error: %v", err)
        return
    }
    s.logger.Printf(string(data))
}
