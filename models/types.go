package models

type TwilioSMSRequest struct {
	From       string `form:"From" json:"From"`
	To         string `form:"To" json:"To"`
	Body       string `form:"Body" json:"Body"`
	MessageSid string `form:"MessageSid" json:"MessageSid"`
}

type DisambiguationSession struct {
	StopOptions []StopOption `json:"stopOptions"`
	CreatedAt   int64        `json:"createdAt"`
}

type StopOption struct {
	FullStopID  string `json:"fullStopId"`
	AgencyName  string `json:"agencyName"`
	StopName    string `json:"stopName"`
	DisplayText string `json:"displayText"`
}

type TwilioVoiceRequest struct {
	From    string `form:"From" json:"From"`
	To      string `form:"To" json:"To"`
	CallSid string `form:"CallSid" json:"CallSid"`
	Digits  string `form:"Digits" json:"Digits,omitempty"`
}

type Stop struct {
	ID              string   `json:"id"`
	Name            string   `json:"name"`
	Direction       string   `json:"direction"`
	Latitude        float64  `json:"lat"`
	Longitude       float64  `json:"lon"`
	RouteShortNames []string `json:"routeShortNames"`
}

type Arrival struct {
	RouteShortName       string `json:"routeShortName"`
	TripHeadsign         string `json:"tripHeadsign"`
	PredictedArrivalTime int64  `json:"predictedArrivalTime"`
	ScheduledArrivalTime int64  `json:"scheduledArrivalTime"`
	MinutesUntilArrival  int    `json:"minutesUntilArrival"`
	Status               string `json:"status"`
}

// OBAArrivalDeparture is one predicted arrival/departure at a stop.
type OBAArrivalDeparture struct {
	RouteShortName       string `json:"routeShortName"`
	TripHeadsign         string `json:"tripHeadsign"`
	PredictedArrivalTime int64  `json:"predictedArrivalTime"`
	ScheduledArrivalTime int64  `json:"scheduledArrivalTime"`
	Status               string `json:"status"`
	// SituationIds references alerts affecting this arrival's trip/route. Present on
	// Puget Sound; treated as optional/non-load-bearing (MVP filters via references).
	SituationIds []string `json:"situationIds"`
}

// OBAReferences holds objects referenced by the response.
type OBAReferences struct {
	Situations []RawSituation `json:"situations"`
}

// OBAStopEntry is the stop payload of an arrivals-and-departures-for-stop response.
type OBAStopEntry struct {
	ArrivalsAndDepartures []OBAArrivalDeparture `json:"arrivalsAndDepartures"`
	StopId                string                `json:"stopId"`
	SituationIds          []string              `json:"situationIds"`
}

// OBAResponseData is the data envelope.
type OBAResponseData struct {
	References OBAReferences `json:"references"`
	Entry      OBAStopEntry  `json:"entry"`
}

type OneBusAwayResponse struct {
	// CurrentTime is the server clock in ms; reference time for active-window filtering.
	CurrentTime int64           `json:"currentTime"`
	Data        OBAResponseData `json:"data"`
	Code        int             `json:"code"`
	Text        string          `json:"text"`
}

type StopData struct {
	Data struct {
		List []struct {
			ID   string  `json:"id"`
			Name string  `json:"name"`
			Lat  float64 `json:"lat"`
			Lon  float64 `json:"lon"`
		} `json:"list"`
	} `json:"data"`
	Code int    `json:"code"`
	Text string `json:"text"`
}

// AgencyCoverageRow is one agency entry from agencies-with-coverage.json data.list.
type AgencyCoverageRow struct {
	AgencyID string  `json:"agencyId"`
	Lat      float64 `json:"lat"`
	LatSpan  float64 `json:"latSpan"`
	Lon      float64 `json:"lon"`
	LonSpan  float64 `json:"lonSpan"`
}

type AgenciesWithCoverageResponse struct {
	Data struct {
		LimitExceeded bool                `json:"limitExceeded"`
		List          []AgencyCoverageRow `json:"list"`
	} `json:"data"`
	Code int    `json:"code"`
	Text string `json:"text"`
}

type CoverageArea struct {
	CenterLat float64
	CenterLon float64
	Radius    float64
}

type VoiceSession struct {
	StopID       string `json:"stopId"`
	MinutesAfter int    `json:"minutesAfter"`
	CreatedAt    int64  `json:"createdAt"`
}

type SMSSession struct {
	LastStopID    string `json:"lastStopId"`
	Language      string `json:"language"`
	LastQueryTime int64  `json:"lastQueryTime"`
	WindowMinutes int    `json:"windowMinutes"`
	// ArrivalHorizonShownMinutes is how many arrivals were already shown in this session (count / slice offset), not a time duration.
	// The JSON name is historical; it is used as a continuation offset so "more" can return the next page of departures.
	ArrivalHorizonShownMinutes int   `json:"arrivalHorizonShownMinutes"`
	CreatedAt                  int64 `json:"createdAt"`
}
