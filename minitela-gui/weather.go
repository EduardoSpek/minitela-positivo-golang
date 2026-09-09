package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"time"
)

// weatherConfig holds the user's location for the Clima screen.
type weatherConfig struct {
	City string `json:"city"`
	// Resolution cache: last successful geocode + forecast.
	Lat       float64 `json:"lat,omitempty"`
	Lon       float64 `json:"lon,omitempty"`
	PlaceName string  `json:"placeName,omitempty"`
}

// DayForecast is one day of the Open-Meteo daily forecast.
type DayForecast struct {
	Date      string // "2024-10-28"
	WMO       int
	IsDay     bool
	Temp      int
	TempMin   int
	TempMax   int
	Weekday   string // short PT-BR label, e.g. "SEG"
	WeekdayFull string
}

func weatherConfigPath() (string, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "minitela-gui.exe", "weather.json"), nil
}

func loadWeatherConfig() weatherConfig {
	var cfg weatherConfig
	p, err := weatherConfigPath()
	if err != nil {
		return cfg
	}
	b, err := os.ReadFile(p)
	if err == nil {
		_ = json.Unmarshal(b, &cfg)
	}
	return cfg
}

func saveWeatherConfig(cfg weatherConfig) error {
	p, err := weatherConfigPath()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return err
	}
	b, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(p, b, 0o600)
}

// GetWeatherConfig returns the saved location config for the Clima screen.
func (a *App) GetWeatherConfig() map[string]interface{} {
	cfg := loadWeatherConfig()
	return map[string]interface{}{
		"city":      cfg.City,
		"placeName": cfg.PlaceName,
	}
}

// SetWeatherConfig stores the city, geocodes it (if it changed materially) and
// refreshes the forecast so the Clima screen updates immediately.
func (a *App) SetWeatherConfig(city string) error {
	city = normalizeText(city)
	if city == "" {
		return fmt.Errorf("informe o nome da cidade")
	}
	cfg := loadWeatherConfig()
	cityChanged := cfg.City != city
	cfg.City = city
	if cityChanged || cfg.PlaceName == "" {
		geo, err := geocodeCity(city)
		if err != nil {
			return err
		}
		cfg.Lat = geo.Lat
		cfg.Lon = geo.Lon
		cfg.PlaceName = geo.DisplayName
	}
	if err := saveWeatherConfig(cfg); err != nil {
		return err
	}
	// Force an immediate refresh of the forecast to (re)fill the screen.
	a.weatherMu.Lock()
	a.weatherLast = nil
	a.weatherMu.Unlock()
	go a.refreshWeather(cfg)
	return nil
}

type geoResult struct {
	Lat         float64
	Lon         float64
	DisplayName string
}

// geocodeCity resolves a city name to coordinates via the Open-Meteo
// geocoding API. displayName falls back to the input when the API omits it.
func geocodeCity(name string) (geoResult, error) {
	url := fmt.Sprintf(
		"https://geocoding-api.open-meteo.com/v1/search?name=%s&count=1&language=pt&format=json",
		urlEscape(name),
	)
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return geoResult{}, err
	}
	req.Header.Set("User-Agent", "MinitelaGo/1.0")
	client := &http.Client{Timeout: 12 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return geoResult{}, fmt.Errorf("geocodificação falhou: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return geoResult{}, fmt.Errorf("geocodificação retornou HTTP %d", resp.StatusCode)
	}
	var out struct {
		Results []struct {
			Latitude  float64 `json:"latitude"`
			Longitude float64 `json:"longitude"`
			Name      string  `json:"name"`
			Country   string  `json:"country"`
		} `json:"results"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return geoResult{}, fmt.Errorf("resposta de geocodificação inválida: %w", err)
	}
	if len(out.Results) == 0 {
		return geoResult{}, fmt.Errorf("cidade não encontrada: %s", name)
	}
	r := out.Results[0]
	display := r.Name
	if r.Country != "" {
		display = r.Name + ", " + r.Country
	}
	return geoResult{Lat: r.Latitude, Lon: r.Longitude, DisplayName: display}, nil
}

// refreshWeather fetches the 5-day forecast and stores it for pushWeatherTags.
func (a *App) refreshWeather(cfg weatherConfig) {
	days, err := fetchForecast(cfg.Lat, cfg.Lon)
	if err != nil {
		return
	}
	a.weatherMu.Lock()
	a.weatherLast = days
	a.weatherFetchedAt = time.Now()
	a.weatherMu.Unlock()
	runtimeEmit(a.ctx, "weather", a.weatherPayload())
}

// weatherPayload exposes the current forecast to the frontend.
func (a *App) weatherPayload() map[string]interface{} {
	cfg := loadWeatherConfig()
	a.weatherMu.Lock()
	days := a.weatherLast
	a.weatherMu.Unlock()
	out := map[string]interface{}{
		"city":      cfg.City,
		"placeName": cfg.PlaceName,
		"days":      []interface{}{},
	}
	if len(days) == 0 {
		return out
	}
	arr := make([]interface{}, 0, len(days))
	for _, d := range days {
		arr = append(arr, map[string]interface{}{
			"date":        d.Date,
			"weekday":     d.Weekday,
			"wmo":         d.WMO,
			"day":         d.IsDay,
			"temp":        d.Temp,
			"tempMin":     d.TempMin,
			"tempMax":     d.TempMax,
			"icon":        wmoToIcon(d.WMO, d.IsDay),
			"condition":   wmoLabel(d.WMO),
		})
	}
	out["days"] = arr
	return out
}

// fetchForecast returns the next 5 days (today included) from Open-Meteo.
// Day 0 reflects the CURRENT conditions (current weather_code/temperature):
// the daily weather_code is the day's dominant (most severe) condition, which
// shows rain all day when only a brief shower is expected while it is sunny
// now. Days 1+ keep the daily forecast values.
func fetchForecast(lat, lon float64) ([]DayForecast, error) {
	url := fmt.Sprintf(
		"https://api.open-meteo.com/v1/forecast?latitude=%s&longitude=%s&current=weather_code,temperature_2m&daily=weather_code,temperature_2m_max,temperature_2m_min,temperature_2m_mean&timezone=auto&forecast_days=5",
		strconv.FormatFloat(lat, 'g', -1, 64),
		strconv.FormatFloat(lon, 'g', -1, 64),
	)
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "MinitelaGo/1.0")
	client := &http.Client{Timeout: 12 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("previsão falhou: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("previsão retornou HTTP %d", resp.StatusCode)
	}
	var out struct {
		Current struct {
			WeatherCode *int     `json:"weather_code"`
			Temp        *float64 `json:"temperature_2m"`
		} `json:"current"`
		Daily struct {
			Time      []string  `json:"time"`
			Weather   []int     `json:"weather_code"`
			TempMax   []float64 `json:"temperature_2m_max"`
			TempMin   []float64 `json:"temperature_2m_min"`
			TempAvg   []float64 `json:"temperature_2m_mean"`
		} `json:"daily"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, fmt.Errorf("resposta de previsão inválida: %w", err)
	}
	n := len(out.Daily.Time)
	if n == 0 {
		return nil, fmt.Errorf("previsão vazia")
	}
	now := time.Now()
	hours := now.Hour()
	isDay := hours >= 6 && hours < 19
	af := out.Daily
	days := make([]DayForecast, 0, n)
	for i := 0; i < n; i++ {
		wmo := 0
		if i < len(af.Weather) {
			wmo = af.Weather[i]
		}
		var tmax, tmin, tavg int
		if i < len(af.TempMax) {
			tmax = int(af.TempMax[i])
		}
		if i < len(af.TempMin) {
			tmin = int(af.TempMin[i])
		}
		if i < len(af.TempAvg) {
			tavg = int(af.TempAvg[i])
		}
		// today uses the actual time-of-day factor; future days default to day.
		dayFactor := isDay
		if i > 0 {
			dayFactor = true
		}
		date := af.Time[i]
		t, _ := time.Parse("2006-01-02", date)
		// Day 0 shows the current conditions; the daily values stay as the
		// min/max range. If the API omits the current block, fall back to
		// the daily values (previous behavior).
		wmoNow, tempNow := wmo, tavg
		if i == 0 {
			if out.Current.WeatherCode != nil {
				wmoNow = *out.Current.WeatherCode
			}
			if out.Current.Temp != nil {
				tempNow = int(*out.Current.Temp)
			}
		}
		days = append(days, DayForecast{
			Date:         date,
			WMO:          wmoNow,
			IsDay:        dayFactor,
			Temp:         tempNow,
			TempMin:      tmin,
			TempMax:      tmax,
			Weekday:      weekdayShort(t),
		})
	}
	return days, nil
}

// wmoToIcon maps an Open-Meteo WMO weather code to the theme's condition
// slide index (Weather_N_Type register). Slide glyphs (theme data.json):
// 0=Sol, 1=Sol+nuvem, 2=Nublado, 3=Chuva, 4=Lua, 5=Lua+nuvem, 6=Neve, 7=nada.
func wmoToIcon(wmo int, day bool) int {
	// clear / mostly clear / partly cloudy
	switch wmo {
	case 0:
		if day {
			return 0 // Sun
		}
		return 4 // Moon
	case 1, 2:
		if day {
			return 1 // Sun behind cloud
		}
		return 5 // Moon behind cloud
	}
	switch {
	case wmo == 3 || wmo == 45 || wmo == 48:
		return 2 // Overcast
	case (wmo >= 51 && wmo <= 67) || (wmo >= 80 && wmo <= 82) ||
		(wmo >= 95 && wmo <= 99):
		return 3 // Drizzle/Rain/Thunderstorm
	case (wmo >= 71 && wmo <= 77) || wmo == 85 || wmo == 86:
		return 6 // Snow
	default:
		return 7 // unknown / transparent
	}
}

// wmoLabel returns a PT-BR description for a WMO code.
func wmoLabel(wmo int) string {
	switch {
	case wmo == 0:
		return "Ensolarado"
	case wmo == 1 || wmo == 2:
		return "Parcialmente nublado"
	case wmo == 3 || wmo == 45 || wmo == 48:
		return "Nublado"
	case wmo >= 51 && wmo <= 57:
		return "Garoa"
	case wmo >= 61 && wmo <= 67:
		return "Chuva"
	case wmo >= 71 && wmo <= 77:
		return "Neve"
	case wmo >= 80 && wmo <= 82:
		return "Pancadas de chuva"
	case wmo == 85 || wmo == 86:
		return "Neve para pancadas"
	case wmo == 95:
		return "Trovoadas"
	case wmo >= 96 && wmo <= 99:
		return "Trovoadas com chuva de granizo"
	default:
		return "Desconhecido"
	}
}

// weekdayShort returns a short uppercase PT-BR weekday label.
func weekdayShort(t time.Time) string {
	switch t.Weekday() {
	case time.Sunday:
		return "DOM"
	case time.Monday:
		return "SEG"
	case time.Tuesday:
		return "TER"
	case time.Wednesday:
		return "QUA"
	case time.Thursday:
		return "QUI"
	case time.Friday:
		return "SEX"
	default:
		return "SAB"
	}
}

func urlEscape(s string) string {
	b := []byte(s)
	const hex = "0123456789ABCDEF"
	var out []byte
	for _, c := range b {
		switch {
		case (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9'):
			out = append(out, c)
		default:
			out = append(out, '%', hex[c>>4], hex[c&0xf])
		}
	}
	return string(out)
}