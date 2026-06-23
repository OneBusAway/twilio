package umami

import "oba-twilio/analytics"

// payload is the top-level Umami /api/send request body.
type payload struct {
	Type    string      `json:"type"`
	Payload payloadBody `json:"payload"`
}

// payloadBody mirrors Umami's event payload. Name is always set (events-only);
// Data is omitted when empty.
type payloadBody struct {
	Website  string                 `json:"website"`
	Hostname string                 `json:"hostname"`
	URL      string                 `json:"url"`
	Name     string                 `json:"name,omitempty"`
	Data     map[string]interface{} `json:"data,omitempty"`
}

// convertEvent maps an analytics.Event to a Umami payload.
func convertEvent(cfg Config, event analytics.Event) payload {
	data := sanitizeData(event.Properties)
	if event.UserID != "" {
		data["user_id"] = event.UserID
	}
	if event.SessionID != "" {
		data["session_id"] = event.SessionID
	}

	body := payloadBody{
		Website:  cfg.WebsiteID,
		Hostname: cfg.Hostname,
		URL:      pathForEvent(event.Name),
		Name:     event.Name,
	}
	if len(data) > 0 {
		body.Data = data
	}

	return payload{Type: "event", Payload: body}
}
