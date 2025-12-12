package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

const (
	statePathEnv = "POWERBOT_STATE"
	testFileEnv  = "POWERBOT_TEST_FILE"
	tokenEnv     = "POWERBOT_TOKEN"
	chatIDEnv    = "POWERBOT_CHAT_ID"
	debugEnv     = "POWERBOT_DEBUG"
	fetchURL     = "https://api.loe.lviv.ua/api/menus?page=1&type=photo-grafic"
	defaultState = "/var/lib/powerbot/state.json"
	kyivTZ       = "Europe/Kyiv"
	groupWater   = "Група 4.1"
	groupPower   = "Група 6.1"
	labelWater   = "*💧 води не буде*"
	labelPower   = "*💡 світла не буде*"
)

type GroupInfo struct {
	Text    string `json:"text"`
	Minutes int    `json:"minutes"`
}

type DayInfo struct {
	Date   string               `json:"date"` // yyyy-mm-dd
	Groups map[string]GroupInfo `json:"groups"`
}

type State struct {
	Days []DayInfo `json:"days"`
}

func main() {
	loc, _ := time.LoadLocation(kyivTZ)
	today := time.Now().In(loc).Truncate(24 * time.Hour)
	datesToCheck := []time.Time{today, today.AddDate(0, 0, 1)}
	debug := os.Getenv(debugEnv) != ""

	htmlBody, err := loadContent()
	if err != nil {
		logf("error fetching: %v", err)
		return
	}
	if debug {
		logf("debug: fetched %d bytes", len(htmlBody))
	}

	parsed, err := parsePage(htmlBody, datesToCheck)
	if err != nil {
		logf("parse error: %v", err)
		return
	}
	logf("parsed %d days (looking for %s and %s)", len(parsed), datesToCheck[0].Format("02.01.2006"), datesToCheck[1].Format("02.01.2006"))
	if len(parsed) == 0 {
		logf("warning: no schedules found for today or tomorrow")
	} else {
		for _, d := range parsed {
			logf("found schedule for %s with %d groups", d.Date, len(d.Groups))
			for k, v := range d.Groups {
				logf("  %s => %s (mins=%d)", k, v.Text, v.Minutes)
			}
		}
	}

	statePath := os.Getenv(statePathEnv)
	if statePath == "" {
		statePath = defaultState
	}
	st, err := loadState(statePath)
	if debug && err != nil {
		logf("debug: loadState error (non-fatal): %v", err)
	}

	token := os.Getenv(tokenEnv)
	chatID := os.Getenv(chatIDEnv)
	if token == "" || chatID == "" {
		logf("warning: POWERBOT_TOKEN or POWERBOT_CHAT_ID not set, skipping Telegram posts")
	}

	for _, day := range parsed {
		prev := findDay(st, day.Date)
		if prev == nil {
			logf("new schedule for %s, posting...", day.Date)
			if token != "" && chatID != "" {
				if err := postSchedule(token, chatID, day, false, false); err != nil {
					logf("post error: %v", err)
				} else {
					logf("posted successfully")
				}
			}
			st = upsertDay(st, day)
			continue
		}

		changed, more := compareDay(*prev, day)
		if changed {
			logf("schedule changed for %s (more=%v), posting update...", day.Date, more)
			if token != "" && chatID != "" {
				if err := postSchedule(token, chatID, day, true, more); err != nil {
					logf("post error: %v", err)
				} else {
					logf("update posted successfully")
				}
			}
			st = upsertDay(st, day)
		} else {
			logf("schedule for %s unchanged, skipping", day.Date)
		}
	}

	st = keepLastTwo(st, datesToCheck)
	if err := saveState(statePath, st); err != nil {
		logf("state save error: %v", err)
	}
}

func loadContent() (string, error) {
	debug := os.Getenv(debugEnv) != ""
	if path := os.Getenv(testFileEnv); path != "" {
		b, err := os.ReadFile(path)
		if debug {
			logf("debug: reading from test file: %s", path)
		}
		return string(b), err
	}
	if debug {
		logf("debug: fetching from URL: %s", fetchURL)
	}
	resp, err := http.Get(fetchURL)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return "", fmt.Errorf("status %d", resp.StatusCode)
	}
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}
	if debug {
		logf("debug: received %d bytes from API", len(b))
	}

	// Parse JSON response
	var apiResponse struct {
		HydraMember []struct {
			MenuItems []struct {
				Name    string `json:"name"`
				RawHtml string `json:"rawHtml"`
			} `json:"menuItems"`
		} `json:"hydra:member"`
	}
	if err := json.Unmarshal(b, &apiResponse); err != nil {
		if debug {
			logf("debug: JSON unmarshal error: %v", err)
			logf("debug: response preview (first 500 chars): %s", string(b[:min(500, len(b))]))
		}
		return "", fmt.Errorf("failed to parse API response: %w", err)
	}

	// Extract rawHtml from menuItems
	for _, member := range apiResponse.HydraMember {
		for _, item := range member.MenuItems {
			if item.RawHtml != "" {
				if debug {
					logf("debug: extracted rawHtml from menu item '%s' (%d bytes)", item.Name, len(item.RawHtml))
				}
				return item.RawHtml, nil
			}
		}
	}

	return "", fmt.Errorf("no rawHtml found in API response")
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// parsePage uses regex-based extraction; assumes stable, simple HTML/text.
func parsePage(body string, dates []time.Time) ([]DayInfo, error) {
	var out []DayInfo
	debug := os.Getenv(debugEnv) != ""
	if debug {
		// Save first 2000 chars for inspection
		preview := body
		if len(preview) > 2000 {
			preview = preview[:2000]
		}
		logf("debug: HTML preview (first 2000 chars):\n%s", preview)
		// Check if we can find the date pattern at all
		datePat := regexp.MustCompile(`Графік погодинних відключень на\s+\d{2}\.\d{2}\.\d{4}`)
		matches := datePat.FindAllString(body, -1)
		logf("debug: found %d date headers: %v", len(matches), matches)
	}
	for _, d := range dates {
		dateTitle := d.Format("02.01.2006")
		if debug {
			logf("debug: looking for date '%s'", dateTitle)
		}
		section := extractSection(body, dateTitle)
		if section == "" {
			if debug {
				logf("debug: no section found for %s", dateTitle)
			}
			continue
		}
		if debug {
			preview := section
			if len(preview) > 500 {
				preview = preview[:500]
			}
			logf("debug: found section for %s (first 500 chars):\n%s", dateTitle, preview)
		}
		groups := map[string]GroupInfo{}
		for _, g := range []string{groupPower, groupWater} {
			txt := extractGroup(section, g)
			if debug {
				if txt == "" {
					logf("debug: group %s not found in section", g)
				} else {
					logf("debug: found group %s: '%s'", g, txt)
				}
			}
			if txt == "" {
				continue
			}
			norm := normalizeText(txt)
			mins := outageMinutes(norm)
			groups[g] = GroupInfo{Text: norm, Minutes: mins}
		}
		if len(groups) > 0 {
			out = append(out, DayInfo{Date: d.Format("2006-01-02"), Groups: groups})
		}
	}
	return out, nil
}

// extractSection grabs text between the date title and the next date title or end.
func extractSection(body, dateTitle string) string {
	// Try with HTML tags first (e.g., <b>Графік погодинних відключень на 12.12.2025</b>)
	pat := regexp.MustCompile(`(?s)<b>Графік погодинних відключень на\s+` + regexp.QuoteMeta(dateTitle) + `</b>(.*?)(?:<b>Графік погодинних відключень на\s+\d{2}\.\d{2}\.\d{4}</b>|$)`)
	m := pat.FindStringSubmatch(body)
	if len(m) >= 2 {
		return m[1]
	}
	// Fallback: try without HTML tags
	pat2 := regexp.MustCompile(`(?s)Графік погодинних відключень на\s+` + regexp.QuoteMeta(dateTitle) + `(.*?)(?:Графік погодинних відключень на\s+\d{2}\.\d{2}\.\d{4}|$)`)
	m2 := pat2.FindStringSubmatch(body)
	if len(m2) >= 2 {
		return m2[1]
	}
	return ""
}

// extractGroup finds the first text after the group label up to a period.
func extractGroup(section, group string) string {
	pat := regexp.MustCompile(regexp.QuoteMeta(group) + `[^\.]*\.?\s*([^\.]*\.)`)
	m := pat.FindStringSubmatch(section)
	if len(m) >= 2 {
		return strings.TrimSpace(m[1])
	}
	// fallback: grab the sentence after the label
	pat2 := regexp.MustCompile(regexp.QuoteMeta(group) + `.*?\.\s*([^.]+\.)`)
	m2 := pat2.FindStringSubmatch(section)
	if len(m2) >= 2 {
		return strings.TrimSpace(m2[1])
	}
	return ""
}

func normalizeText(s string) string {
	s = strings.TrimSpace(s)
	s = strings.TrimPrefix(s, "—")
	s = strings.TrimSpace(s)
	s = strings.ReplaceAll(s, "\u00a0", " ")
	s = strings.ReplaceAll(s, "  ", " ")
	if strings.Contains(s, "Електроенергія є") {
		return "буде!!!!"
	}
	s = strings.TrimSuffix(s, ".")
	return s
}

func outageMinutes(text string) int {
	// expect "немає з HH:MM до HH:MM"
	re := regexp.MustCompile(`з\s+(\d{2}):(\d{2})\s+до\s+(\d{2}):(\d{2})`)
	m := re.FindStringSubmatch(text)
	if len(m) != 5 {
		return 0
	}
	h1, _ := time.Parse("15:04", m[1]+":"+m[2])
	h2, _ := time.Parse("15:04", m[3]+":"+m[4])
	return int(h2.Sub(h1).Minutes())
}

func loadState(path string) (State, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return State{}, err
	}
	var s State
	err = json.Unmarshal(b, &s)
	return s, err
}

func saveState(path string, st State) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp := path + ".tmp"
	b, _ := json.MarshalIndent(st, "", "  ")
	if err := os.WriteFile(tmp, b, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

func findDay(st State, date string) *DayInfo {
	for i := range st.Days {
		if st.Days[i].Date == date {
			return &st.Days[i]
		}
	}
	return nil
}

func upsertDay(st State, day DayInfo) State {
	found := false
	for i := range st.Days {
		if st.Days[i].Date == day.Date {
			st.Days[i] = day
			found = true
			break
		}
	}
	if !found {
		st.Days = append(st.Days, day)
	}
	return st
}

func keepLastTwo(st State, refs []time.Time) State {
	cutoff := map[string]bool{}
	for _, d := range refs {
		cutoff[d.Format("2006-01-02")] = true
		cutoff[d.AddDate(0, 0, -1).Format("2006-01-02")] = true
	}
	var kept []DayInfo
	for _, d := range st.Days {
		if cutoff[d.Date] {
			kept = append(kept, d)
		}
	}
	st.Days = kept
	return st
}

func compareDay(old, cur DayInfo) (changed bool, more bool) {
	for _, g := range []string{groupPower, groupWater} {
		o, okO := old.Groups[g]
		n, okN := cur.Groups[g]
		if !okN && !okO {
			continue
		}
		if !okO || !okN || o.Text != n.Text {
			if n.Minutes > o.Minutes {
				more = true
			}
			changed = true
		}
	}
	return
}

func postSchedule(token, chatID string, day DayInfo, isUpdate, more bool) error {
	title := fmt.Sprintf("графік на %s", toDM(day.Date))
	if isUpdate {
		if more {
			title = fmt.Sprintf("upd. 😩 на %s", toDM(day.Date))
		} else {
			title = fmt.Sprintf("upd. 🍾 на %s", toDM(day.Date))
		}
	}
	var lines []string
	lines = append(lines, fmt.Sprintf("*%s*", title))
	lines = append(lines, formatLine(day, groupPower, labelPower))
	lines = append(lines, formatLine(day, groupWater, labelWater))
	msg := strings.Join(lines, "\n")
	return sendTelegram(token, chatID, msg)
}

func formatLine(day DayInfo, group, label string) string {
	if g, ok := day.Groups[group]; ok {
		return fmt.Sprintf("%s: %s", label, g.Text)
	}
	return fmt.Sprintf("%s: н/д", label)
}

func toDM(date string) string {
	t, _ := time.Parse("2006-01-02", date)
	return t.Format("02.01")
}

func sendTelegram(token, chatID, text string) error {
	form := fmt.Sprintf("chat_id=%s&text=%s&parse_mode=Markdown", chatID, urlEncode(text))
	resp, err := http.Post("https://api.telegram.org/bot"+token+"/sendMessage", "application/x-www-form-urlencoded", strings.NewReader(form))
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		return fmt.Errorf("telegram status %d: %s", resp.StatusCode, string(body))
	}
	return nil
}

func urlEncode(s string) string {
	var buf bytes.Buffer
	for i := 0; i < len(s); i++ {
		c := s[i]
		if ('a' <= c && c <= 'z') || ('A' <= c && c <= 'Z') || ('0' <= c && c <= '9') || c == '-' || c == '_' || c == '.' || c == '~' || c == '*' || c == '(' || c == ')' || c == '\'' {
			buf.WriteByte(c)
		} else if c == ' ' {
			buf.WriteByte('+')
		} else {
			buf.WriteString(fmt.Sprintf("%%%02X", c))
		}
	}
	return buf.String()
}

func logf(format string, args ...interface{}) {
	fmt.Fprintf(os.Stderr, format+"\n", args...)
}
