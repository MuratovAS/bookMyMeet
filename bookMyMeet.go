package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"
	_ "time/tzdata"
	"golang.org/x/time/rate"
	"github.com/emersion/go-ical"
	"github.com/emersion/go-webdav/caldav"
	"github.com/google/uuid"
	"github.com/gorilla/mux"
)

var (
	DaysAvailableForBooking = getEnvInt("DAYS_AVAILABLE", 28)      // Number of days available for booking
	WorkDayStartHour        = getEnvInt("WORKDAY_START", 8)        // Workday start hour (UTC)
	WorkDayEndHour          = getEnvInt("WORKDAY_END", 19)         // Workday end hour (UTC)
	
	CalDAVServerURL          = getEnvStr("CALDAV_SERVER_URL", "")
	CalDAVUsername           = getEnvStr("CALDAV_USERNAME", "")
	CalDAVPassword           = getEnvStr("CALDAV_PASSWORD", "")
	CalDAVCalendar           = getEnvStr("CALDAV_CALENDAR", "")
	CalDAVAdditionalCalendars = getEnvStrSlice("CALDAV_ADDITIONAL_CALENDARS", "")
)

func getEnvStr(key, defaultValue string) string {
	if value, exists := os.LookupEnv(key); exists {
		return value
	}
	return defaultValue
}

func getEnvInt(key string, defaultValue int) int {
	if value, exists := os.LookupEnv(key); exists {
		if intValue, err := strconv.Atoi(value); err == nil {
			return intValue
		}
	}
	return defaultValue
}

func getEnvStrSlice(key, defaultValue string) []string {
	if value, exists := os.LookupEnv(key); exists {
		return strings.Split(value, ",")
	}
	return strings.Split(defaultValue, ",")
}

type BookingRequest struct {
	Date        string `json:"date"`
	Time        string `json:"time"`
	Topic       string `json:"topic"`
	FullName    string `json:"fullName"`
	ContactInfo string `json:"contactInfo"`
	CSRFToken   string `json:"_csrf"`
}

type BookingResponse struct {
	Success bool   `json:"success"`
	Code    string `json:"code,omitempty"`
	Error   string `json:"error,omitempty"`
}

type CancelRequest struct {
	Code      string `json:"code"`
	CSRFToken string `json:"_csrf"`
}

type CalDAVConfig struct {
	ServerURL          string
	Username           string
	Password           string
	Calendar           string
	AdditionalCalendars []string
}

var caldavConfig = CalDAVConfig{
	ServerURL:          CalDAVServerURL,
	Username:           CalDAVUsername,
	Password:           CalDAVPassword,
	Calendar:           CalDAVCalendar,
	AdditionalCalendars: CalDAVAdditionalCalendars,
}

var (
	limiter = rate.NewLimiter(rate.Every(time.Minute), 100) // 100 requests per minute
	bookingCodes = make(map[string]string) // In production use a database
	caldavClient *caldav.Client
	
	eventsCache      map[string][]*ical.Component // Events cache by date
	eventsCacheMutex sync.RWMutex
	eventsLastSync   time.Time
)


func generateAvailableSlotsDirect() map[string][]string {
	slots := make(map[string][]string)
	now := time.Now()
	var datesToCheck []string

	// Collect all dates to check
	for i := 0; i < DaysAvailableForBooking; i++ {
		date := now.AddDate(0, 0, i)
		dateStr := date.Format("2006-01-02")
		
		// Skip weekends
		if date.Weekday() == time.Sunday {
			continue
		}
		datesToCheck = append(datesToCheck, dateStr)
	}

	// Load events for all days at once
	syncEventsCache(datesToCheck)

	// Generate slots for each day
	for _, dateStr := range datesToCheck {
		// Check events cache
		eventsCacheMutex.RLock()
		events := eventsCache[dateStr]
		eventsCacheMutex.RUnlock()

		// Working hours
		var daySlots []string
		for hour := WorkDayStartHour; hour < WorkDayEndHour; hour++ {
			timeStr := fmt.Sprintf("%02d:00", hour)
			datetime, err := time.Parse("2006-01-02 15:04", dateStr+" "+timeStr)
			if err != nil {
				continue
			}

			slotStart := datetime
			slotEnd := datetime.Add(time.Hour)
			slotFree := true

			// Check events
			for _, event := range events {
				dtstart := event.Props.Get(ical.PropDateTimeStart)
				if dtstart == nil {
					continue
				}

				eventTime, err := dtstart.DateTime(time.UTC)
				if err != nil {
					continue
				}

				dtend := event.Props.Get(ical.PropDateTimeEnd)
				if dtend == nil {
					dtend = &ical.Prop{Value: eventTime.Add(time.Hour).Format("20060102T150405Z")}
				}
				
				endTime, err := dtend.DateTime(time.UTC)
				if err != nil {
					continue
				}
				
				if eventTime.Before(slotEnd) && endTime.After(slotStart) {
					slotFree = false
					break
				}
			}

			if slotFree {
				daySlots = append(daySlots, timeStr)
			}
		}

		if len(daySlots) > 0 {
			slots[dateStr] = daySlots
		}
	}

	return slots
}

func rateLimit(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !limiter.Allow() {
			http.Error(w, "Too many requests", http.StatusTooManyRequests)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func initCalDAVClient() {
	httpClient := &http.Client{
		Transport: &basicAuthTransport{
			Username: caldavConfig.Username,
			Password: caldavConfig.Password,
			Base:     http.DefaultTransport,
		},
		Timeout: 10 * time.Second,
	}

	var err error
	caldavClient, err = caldav.NewClient(httpClient, caldavConfig.ServerURL)
	if err != nil {
		log.Fatalf("Error initializing CalDAV client: %v", err)
	}

	// Verify calendar availability
	ctx := context.Background()
	_, err = caldavClient.FindCalendars(ctx, "")
	if err != nil {
		log.Fatalf("Error accessing calendar: %v", err)
	}

	log.Println("CalDAV client successfully initialized and connected")
}

type basicAuthTransport struct {
	Username string
	Password string
	Base     http.RoundTripper
}

func (t *basicAuthTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	req.SetBasicAuth(t.Username, t.Password)
	return t.Base.RoundTrip(req)
}

func main() {
	// Initialize CalDAV client
	initCalDAVClient()

	r := mux.NewRouter()
	r.Use(rateLimit)

	// Static files
	r.PathPrefix("/static/").Handler(http.StripPrefix("/static/", http.FileServer(http.Dir("./static/"))))

	// CSRF token endpoint
	r.HandleFunc("/api/csrf-token", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{
			"token": uuid.New().String(),
		})
	}).Methods("GET")

	// API endpoints
	r.HandleFunc("/api/available", availableSlots).Methods("GET", "OPTIONS")
	r.HandleFunc("/api/booking", bookingSlot).Methods("POST", "OPTIONS")
	r.HandleFunc("/api/cancel", cancelSlot).Methods("POST", "OPTIONS")

	// Main page
	r.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		http.ServeFile(w, r, "./static/index.html")
	})

	fmt.Println("Server started at http://0.0.0.0:5000")
	log.Fatal(http.ListenAndServe("0.0.0.0:5000", r))
}

func availableSlots(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Access-Control-Allow-Methods", "GET, OPTIONS")
	w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization")
	w.Header().Set("Access-Control-Allow-Credentials", "true")

	// Handle preflight requests
	if r.Method == "OPTIONS" {
		return
	}

	// Generate slots directly
	slots := generateAvailableSlotsDirect()

	if err := json.NewEncoder(w).Encode(slots); err != nil {
		log.Printf("JSON encoding error: %v", err)
		http.Error(w, "Server error", http.StatusInternalServerError)
		return
	}
}


func loadEventsForDate(date string) ([]*ical.Component, error) {
	// Parse date
	day, err := time.Parse("2006-01-02", date)
	if err != nil {
		return nil, err
	}

	startOfDay := time.Date(day.Year(), day.Month(), day.Day(), 0, 0, 0, 0, day.Location())
	endOfDay := startOfDay.Add(24 * time.Hour)

	query := &caldav.CalendarQuery{
		CompFilter: caldav.CompFilter{
			Name: "VCALENDAR",
			Comps: []caldav.CompFilter{{
				Name:  "VEVENT",
				Start: startOfDay,
				End:   endOfDay,
			}},
		},
	}

	var allEvents []*ical.Component
	calendarsToCheck := append([]string{caldavConfig.Calendar}, caldavConfig.AdditionalCalendars...)

	// Use WaitGroup for parallel calendar processing
	var wg sync.WaitGroup
	eventsChan := make(chan []*ical.Component, len(calendarsToCheck))
	errChan := make(chan error, len(calendarsToCheck))
	ctx := context.Background()

	for _, calendar := range calendarsToCheck {
		wg.Add(1)
		go func(cal string) {
			defer wg.Done()
			
			calendarObjects, err := caldavClient.QueryCalendar(ctx, cal, query)
			if err != nil {
				log.Printf("Error querying calendar %s: %v", cal, err)
				errChan <- err
				return
			}

			var events []*ical.Component
			for _, obj := range calendarObjects {
				if obj.Data != nil {
					for _, component := range obj.Data.Children {
						if component.Name == ical.CompEvent {
							events = append(events, component)
						}
					}
				}
			}
			eventsChan <- events
		}(calendar)
	}

	// Wait for all goroutines to complete
	wg.Wait()
	close(eventsChan)
	close(errChan)

	// Collect results
	for events := range eventsChan {
		allEvents = append(allEvents, events...)
	}

	// If there were errors but we have some events - still return them
	if len(allEvents) > 0 {
		return allEvents, nil
	}

	// If failed to get any events at all, return error
	if len(errChan) > 0 {
		return nil, <-errChan
	}

	return allEvents, nil
}

func syncEventsCache(dates []string) {
	eventsCacheMutex.Lock()
	defer eventsCacheMutex.Unlock()

	if eventsCache == nil {
		eventsCache = make(map[string][]*ical.Component)
	}

	// Use WaitGroup for parallel date processing
	var wg sync.WaitGroup
	results := make(chan struct {
		date   string
		events []*ical.Component
		err    error
	}, len(dates))

	for _, date := range dates {
		wg.Add(1)
		go func(d string) {
			defer wg.Done()
			events, err := loadEventsForDate(d)
			results <- struct {
				date   string
				events []*ical.Component
				err    error
			}{d, events, err}
		}(date)
	}

	// Wait for all goroutines to complete
	go func() {
		wg.Wait()
		close(results)
	}()

	// Collect results
	for res := range results {
		if res.err != nil {
			log.Printf("Error loading events for date %s: %v", res.date, res.err)
			continue
		}
		eventsCache[res.date] = res.events
	}

	eventsLastSync = time.Now()
}

func bookingSlot(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Access-Control-Allow-Methods", "POST, OPTIONS")
	w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization, X-CSRF-Token")
	w.Header().Set("Access-Control-Allow-Credentials", "true")

	// Handle preflight requests
	if r.Method == "OPTIONS" {
		return
	}

	// Verify CSRF token
	clientToken := r.Header.Get("X-CSRF-Token")
	if clientToken == "" {
		json.NewEncoder(w).Encode(BookingResponse{
			Success: false,
			Error:   "CSRF token missing",
		})
		return
	}

	var booking BookingRequest
	if err := json.NewDecoder(r.Body).Decode(&booking); err != nil {
		json.NewEncoder(w).Encode(BookingResponse{
			Success: false,
			Error:   "Invalid data format",
		})
		return
	}

	// Verify CSRF token matches
	if booking.CSRFToken != clientToken {
		json.NewEncoder(w).Encode(BookingResponse{
			Success: false,
			Error:   "Invalid CSRF token",
		})
		return
	}

	// Validation
	if booking.Topic == "" || booking.FullName == "" || booking.ContactInfo == "" {
		json.NewEncoder(w).Encode(BookingResponse{
			Success: false,
			Error:   "All fields are required",
		})
		return
	}

	// Create event in CalDAV
	code := uuid.New().String()[:8]
	log.Printf("Creating booking with code: %s", code)

	if err := createCalDAVEvent(booking, code); err != nil {
		log.Printf("Error creating CalDAV event: %v", err)
		json.NewEncoder(w).Encode(BookingResponse{
			Success: false,
			Error:   "Booking creation error: " + err.Error(),
		})
		return
	}

	// Save cancellation code
	eventId := fmt.Sprintf("%s-%s", booking.Date, booking.Time)
	bookingCodes[code] = eventId
	log.Printf("Booking successfully created with code: %s; EID %s", code, eventId)

	json.NewEncoder(w).Encode(BookingResponse{
		Success: true,
		Code:    code,
	})
}

func cancelSlot(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Access-Control-Allow-Methods", "POST, OPTIONS")
	w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization, X-CSRF-Token")
	w.Header().Set("Access-Control-Allow-Credentials", "true")

	// Handle preflight requests
	if r.Method == "OPTIONS" {
		return
	}

	// Verify CSRF token
	clientToken := r.Header.Get("X-CSRF-Token")
	if clientToken == "" {
		json.NewEncoder(w).Encode(BookingResponse{
			Success: false,
			Error:   "CSRF token missing",
		})
		return
	}

	var cancel CancelRequest
	if err := json.NewDecoder(r.Body).Decode(&cancel); err != nil {
		json.NewEncoder(w).Encode(BookingResponse{
			Success: false,
			Error:   "Invalid data format",
		})
		return
	}

	// Verify CSRF token matches
	if cancel.CSRFToken != clientToken {
		json.NewEncoder(w).Encode(BookingResponse{
			Success: false,
			Error:   "Invalid CSRF token",
		})
		return
	}

	if cancel.Code == "" {
		json.NewEncoder(w).Encode(BookingResponse{
			Success: false,
			Error:   "Cancellation code missing",
		})
		return
	}

	eventId, exists := bookingCodes[cancel.Code]
	if !exists {
		json.NewEncoder(w).Encode(BookingResponse{
			Success: false,
			Error:   "Invalid cancellation code",
		})
		return
	}

	// Delete event from CalDAV
	if err := deleteCalDAVEvent(eventId); err != nil {
		json.NewEncoder(w).Encode(BookingResponse{
			Success: false,
			Error:   "Cancellation error",
		})
		return
	}

	delete(bookingCodes, cancel.Code)

	json.NewEncoder(w).Encode(BookingResponse{
		Success: true,
	})
}

func createCalDAVEvent(booking BookingRequest, code string) error {
	// Parse date and time
	datetime, err := time.Parse("2006-01-02 15:04", booking.Date+" "+booking.Time)
	if err != nil {
		log.Printf("Error parsing date/time: %v", err)
		return fmt.Errorf("invalid date or time format")
	}

	log.Printf("Creating event: %s %s for %s", booking.Date, booking.Time, booking.FullName)

	// If CalDAV client is unavailable, return error
	if caldavClient == nil {
		return fmt.Errorf("CalDAV client unavailable")
	}

	// Get calendar list to determine correct path
	ctx := context.Background()
	calendars, err := caldavClient.FindCalendars(ctx, "")
	if err != nil {
		log.Printf("Error getting calendars: %v", err)
		return nil
	}

	var calendarPath string
	for _, cal := range calendars {
		log.Printf("Found calendar: %s", cal.Path)
		if strings.Contains(cal.Path, "default") || len(calendars) == 1 {
			calendarPath = cal.Path
			break
		}
	}

	if calendarPath == "" && len(calendars) > 0 {
		calendarPath = calendars[0].Path
	}

	if calendarPath == "" {
		log.Println("No suitable calendar found")
		return nil
	}

	log.Printf("Using calendar: %s", calendarPath)

	// Create iCal event
	cal := ical.NewCalendar()
	cal.Props.SetText(ical.PropVersion, "2.0")
	cal.Props.SetText(ical.PropProductID, "-//Book my meet//EN")

	event := ical.NewEvent()
	event.Props.SetText(ical.PropUID, code+"@BookMyMeet")
	event.Props.SetDateTime(ical.PropDateTimeStamp, time.Now().UTC())
	event.Props.SetDateTime(ical.PropDateTimeStart, datetime.UTC())
	event.Props.SetDateTime(ical.PropDateTimeEnd, datetime.Add(time.Hour).UTC())
	event.Props.SetText(ical.PropSummary, booking.Topic)

	description := fmt.Sprintf("Who are you?: %s\nContact method: %s\nCancellation code: %s",
		booking.FullName, booking.ContactInfo, code)
	event.Props.SetText(ical.PropDescription, description)
	event.Props.SetText(ical.PropStatus, "CONFIRMED")

	cal.Children = append(cal.Children, event.Component)

	// Create CalDAV event with correct path
	eventPath := calendarPath + code + ".ics"
	log.Printf("Attempting to create event at path: %s", eventPath)

	_, err = caldavClient.PutCalendarObject(ctx, eventPath, cal)
	if err != nil {
		log.Printf("Error creating CalDAV event: %v", err)
		log.Println("Event saved locally but not synced with CalDAV")
		return nil
	}

	log.Printf("Event successfully created in CalDAV with UID: %s", code)
	return nil
}

func deleteCalDAVEvent(eventId string) error {
	log.Printf("Deleting CalDAV event: %s", eventId)

	// If CalDAV client is unavailable, return error
	if caldavClient == nil {
		return fmt.Errorf("CalDAV client unavailable")
	}

	// Find event code by eventId
	var code string
	for c, eid := range bookingCodes {
		if eid == eventId {
			code = c
			break
		}
	}

	if code == "" {
		return fmt.Errorf("no code found for event: %s", eventId)
	}

	// Get correct calendar path
	ctx := context.Background()
	calendars, err := caldavClient.FindCalendars(ctx, "")
	if err != nil {
		log.Printf("Error getting calendars for deletion: %v", err)
		return nil
	}

	var calendarPath string
	for _, cal := range calendars {
		if strings.Contains(cal.Path, "default") || len(calendars) == 1 {
			calendarPath = cal.Path
			break
		}
	}

	if calendarPath == "" && len(calendars) > 0 {
		calendarPath = calendars[0].Path
	}

	if calendarPath == "" {
		log.Println("No calendar found for deletion")
		return nil
	}

	// Delete event from CalDAV
	eventPath := calendarPath + code + ".ics"
	log.Printf("Attempting to delete event at path: %s", eventPath)

	err = caldavClient.RemoveAll(ctx, eventPath)
	if err != nil {
		log.Printf("Error deleting event from CalDAV: %v", err)
		return nil
	}

	log.Printf("Event successfully deleted from CalDAV: %s", code)
	return nil
}
