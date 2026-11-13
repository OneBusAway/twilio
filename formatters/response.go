package formatters

import (
	"encoding/xml"
	"fmt"
	"strings"

	"oba-twilio/localization"
	"oba-twilio/models"
)

func FormatSMSResponse(arrivals []models.Arrival, stopName string, lm *localization.LocalizationManager, language string) string {
	if len(arrivals) == 0 {
		if lm != nil {
			if msg := localizedOrEmpty(lm.GetString("sms.no_arrivals", language), "sms.no_arrivals"); msg != "" {
				return msg
			}
		}
		return "No upcoming arrivals found for this stop."
	}

	var response strings.Builder
	stopLabel := "Stop"
	if lm != nil {
		if localized := localizedOrEmpty(lm.GetString("sms.arrival.stop_label", language), "sms.arrival.stop_label"); localized != "" {
			stopLabel = localized
		}
	}

	if stopName != "" {
		fmt.Fprintf(&response, "%s: %s\n", stopLabel, stopName)
	}

	for i, arrival := range arrivals {
		if i >= 3 {
			break
		}

		timeText := formatArrivalTimeLocalized(arrival.MinutesUntilArrival, lm, language)
		routeLine := ""
		if lm != nil {
			routeLine = localizedOrEmpty(
				lm.GetString("sms.arrival.route_to", language, arrival.RouteShortName, arrival.TripHeadsign, timeText),
				"sms.arrival.route_to",
			)
		}
		if routeLine == "" {
			routeLine = fmt.Sprintf("Route %s to %s: %s", arrival.RouteShortName, arrival.TripHeadsign, timeText)
		}
		response.WriteString(routeLine + "\n")
	}

	return strings.TrimSpace(response.String())
}

func localizedOrEmpty(value, key string) string {
	if value == "" || value == key {
		return ""
	}
	return value
}

// RouteGroup represents arrivals grouped by route and headsign
type RouteGroup struct {
	RouteShortName string
	TripHeadsign   string
	ArrivalTimes   []int
}

// groupArrivalsByRoute groups arrivals by route short name and headsign
func groupArrivalsByRoute(arrivals []models.Arrival) []RouteGroup {
	groups := make(map[string]*RouteGroup)
	var groupOrder []string

	for _, arrival := range arrivals {
		key := arrival.RouteShortName + "|" + arrival.TripHeadsign
		if group, exists := groups[key]; exists {
			group.ArrivalTimes = append(group.ArrivalTimes, arrival.MinutesUntilArrival)
		} else {
			groups[key] = &RouteGroup{
				RouteShortName: arrival.RouteShortName,
				TripHeadsign:   arrival.TripHeadsign,
				ArrivalTimes:   []int{arrival.MinutesUntilArrival},
			}
			groupOrder = append(groupOrder, key)
		}
	}

	result := make([]RouteGroup, len(groupOrder))
	for i, key := range groupOrder {
		result[i] = *groups[key]
	}
	return result
}

func FormatVoiceResponse(arrivals []models.Arrival, stopName string, lm *localization.LocalizationManager, language string) string {
	if len(arrivals) == 0 {
		return lm.GetString("voice.arrival.no_arrivals", language)
	}

	var response strings.Builder

	if stopName != "" {
		response.WriteString(lm.GetString("voice.arrival.arrivals_for", language, stopName))
		response.WriteString(" ")
	}

	// Group arrivals by route
	routeGroups := groupArrivalsByRoute(arrivals)

	for _, group := range routeGroups {
		// Format "Route X to Y"
		routeText := lm.GetString("voice.arrival.route_to", language, group.RouteShortName, group.TripHeadsign)
		response.WriteString(routeText)
		response.WriteString(" ")

		// Format the arrival times
		for i, minutes := range group.ArrivalTimes {
			if i > 0 {
				response.WriteString(", ")
			}
			timeText := formatArrivalTimeVoiceLocalized(minutes, lm, language)
			response.WriteString(timeText)
		}
		response.WriteString(". ")
	}

	return response.String()
}

// SMSStatusCallbackPath is where Twilio reports the delivery status of each reply.
const SMSStatusCallbackPath = "/sms/status"

type TwiMLSMSMessage struct {
	Body           string `xml:",chardata"`
	StatusCallback string `xml:"statusCallback,attr,omitempty"`
}

type TwiMLSMSResponse struct {
	XMLName string          `xml:"Response"`
	Message TwiMLSMSMessage `xml:"Message"`
}

func GenerateTwiMLSMS(message string) (string, error) {
	twiml := TwiMLSMSResponse{
		Message: TwiMLSMSMessage{Body: message, StatusCallback: SMSStatusCallbackPath},
	}

	output, err := xml.MarshalIndent(twiml, "", "  ")
	if err != nil {
		return "", err
	}

	return xml.Header + string(output), nil
}

func formatArrivalTime(minutes int) string {
	if minutes <= 0 {
		return "Now"
	} else if minutes == 1 {
		return "1 min"
	} else {
		return fmt.Sprintf("%d min", minutes)
	}
}

func formatArrivalTimeLocalized(minutes int, lm *localization.LocalizationManager, language string) string {
	if lm != nil {
		if minutes <= 0 {
			if nowText := localizedOrEmpty(lm.GetString("voice.time.now", language), "voice.time.now"); nowText != "" {
				return nowText
			}
		} else if minutes == 1 {
			if oneMin := localizedOrEmpty(lm.GetString("voice.time.minute", language), "voice.time.minute"); oneMin != "" {
				return oneMin
			}
		} else {
			if manyMin := localizedOrEmpty(lm.GetString("voice.time.minutes", language, minutes), "voice.time.minutes"); manyMin != "" {
				return manyMin
			}
		}
	}
	return formatArrivalTime(minutes)
}

func formatArrivalTimeVoice(minutes int) string {
	if minutes <= 0 {
		return "arriving now"
	} else if minutes == 1 {
		return "in 1 minute"
	} else {
		return fmt.Sprintf("in %d minutes", minutes)
	}
}

func formatArrivalTimeVoiceLocalized(minutes int, lm *localization.LocalizationManager, language string) string {
	if minutes <= 0 {
		return lm.GetString("voice.arrival.arriving_now", language)
	} else if minutes == 1 {
		return lm.GetString("voice.arrival.in_one_minute", language)
	} else {
		return lm.GetString("voice.arrival.in_minutes", language, minutes)
	}
}

// ExtractStopID returns the first whitespace-delimited token of message.
// The token is not validated here; callers must use validation.ValidateStopID (or equivalent) before using it as a stop ID.
func ExtractStopID(message string) string {
	message = strings.TrimSpace(message)

	if message == "" {
		return ""
	}

	fields := strings.Fields(message)
	if len(fields) == 0 {
		return ""
	}

	// For stop ID extraction we now trust the first token and let
	// validation.ValidateStopID enforce the detailed rules (length, charset, security).
	return fields[0]
}

func IsDisambiguationChoice(message string) int {
	message = strings.TrimSpace(message)

	// Support 1-99 for better scalability
	if len(message) >= 1 && len(message) <= 2 {
		// Check if all characters are digits
		for _, char := range message {
			if char < '0' || char > '9' {
				return 0
			}
		}

		// Parse the number
		if num := parseInteger(message); num >= 1 && num <= 99 {
			return num
		}
	}

	return 0
}

func parseInteger(s string) int {
	result := 0
	for _, char := range s {
		// Check for overflow before multiplication
		if result > (2147483647-int(char-'0'))/10 {
			return 0 // Return 0 for overflow to indicate invalid choice
		}
		result = result*10 + int(char-'0')
	}
	return result
}

func FormatDisambiguationMessage(stopOptions []models.StopOption, originalStopID string) string {
	if len(stopOptions) == 0 {
		return fmt.Sprintf("No stops found for ID %s.", originalStopID)
	}

	if len(stopOptions) == 1 {
		return ""
	}

	var response strings.Builder
	fmt.Fprintf(&response, "Multiple stops found for %s:\n", originalStopID)

	for i, option := range stopOptions {
		fmt.Fprintf(&response, "%d) %s\n", i+1, option.DisplayText)
	}

	response.WriteString("Reply with the number to choose.")

	return response.String()
}
