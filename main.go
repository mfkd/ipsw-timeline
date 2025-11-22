package main

import (
	"encoding/xml"
	"flag"
	"fmt"
	"html"
	"io"
	"net/http"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"
)

const (
	defaultFeedURL = "https://ipsw.me/timeline.rss"
	defaultLimit   = 15
	defaultTimeout = 10
	defaultColor   = "auto"
)

type rawRSS struct {
	Channel rawChannel `xml:"channel"`
}

type rawChannel struct {
	Items []rawItem `xml:"item"`
}

type rawItem struct {
	Title       string `xml:"title"`
	Link        string `xml:"link"`
	PubDate     string `xml:"pubDate"`
	GUID        string `xml:"guid"`
	Description string `xml:"description"`
}

type Item struct {
	Title          string
	Link           string
	PubDate        time.Time
	GUID           string
	Description    string
	PlatformKey    string
	PlatformLabel  string
	Version        string
	Build          string
	DeviceOrNotes  string
	PreRelease     bool
	RawDevice      string
	Notes          string
	DisplayDate    string
	DisplayVersion string
}

type Config struct {
	FeedURL  string
	Limit    int
	Contains string
	Timeout  time.Duration
	Color    string
}

type colorizer struct {
	enabled bool
}

func (c colorizer) wrap(code string, s string) string {
	if !c.enabled || code == "" || s == "" {
		return s
	}
	return "\033[" + code + "m" + s + "\033[0m"
}

func (c colorizer) dim(s string) string {
	return c.wrap("2", s)
}

func (c colorizer) color(code string, s string) string {
	return c.wrap(code, s)
}

func main() {
	cfg := parseFlags()

	data, err := fetchFeed(cfg.FeedURL, cfg.Timeout)
	if err != nil {
		fmt.Fprintf(os.Stderr, "fetch error (%s): %v\n", cfg.FeedURL, err)
		os.Exit(1)
	}

	rawItems, err := parseFeed(data)
	if err != nil {
		fmt.Fprintf(os.Stderr, "parse error (%s): %v\n", cfg.FeedURL, err)
		os.Exit(1)
	}

	items := make([]Item, 0, len(rawItems))
	for _, r := range rawItems {
		items = append(items, normalizeItem(r))
	}

	filtered := filterItems(items, cfg.Contains)
	sort.Slice(filtered, func(i, j int) bool {
		return filtered[i].PubDate.After(filtered[j].PubDate)
	})

	if cfg.Limit > 0 && len(filtered) > cfg.Limit {
		filtered = filtered[:cfg.Limit]
	}

	if len(filtered) == 0 {
		return
	}

	enableColor := shouldEnableColor(cfg.Color)
	renderTable(filtered, enableColor, os.Stdout)
}

func parseFlags() Config {
	flagSet := flag.CommandLine

	feedURL := flagSet.String("feed-url", defaultFeedURL, "RSS feed URL")
	flagSet.StringVar(feedURL, "f", defaultFeedURL, "RSS feed URL (shorthand)")

	limit := flagSet.Int("limit", defaultLimit, "Number of entries to show")
	flagSet.IntVar(limit, "l", defaultLimit, "Number of entries to show (shorthand)")

	contains := flagSet.String("contains", "", "Case-insensitive substring filter on title")
	flagSet.StringVar(contains, "c", "", "Case-insensitive substring filter on title (shorthand)")

	timeoutSec := flagSet.Int("timeout", defaultTimeout, "HTTP timeout in seconds")
	flagSet.IntVar(timeoutSec, "t", defaultTimeout, "HTTP timeout in seconds (shorthand)")

	color := flagSet.String("color", defaultColor, "Color output: auto|always|never")
	flagSet.StringVar(color, "C", defaultColor, "Color output: auto|always|never (shorthand)")

	flagSet.Parse(os.Args[1:])

	cfg := Config{
		FeedURL:  strings.TrimSpace(*feedURL),
		Limit:    *limit,
		Contains: strings.TrimSpace(*contains),
		Timeout:  time.Duration(*timeoutSec) * time.Second,
		Color:    strings.ToLower(strings.TrimSpace(*color)),
	}

	if cfg.FeedURL == "" {
		fmt.Fprintln(os.Stderr, "feed-url cannot be empty")
		os.Exit(1)
	}

	switch cfg.Color {
	case "auto", "always", "never":
	default:
		fmt.Fprintln(os.Stderr, "invalid color mode: use auto, always, or never")
		os.Exit(1)
	}

	return cfg
}

func fetchFeed(url string, timeout time.Duration) ([]byte, error) {
	client := &http.Client{Timeout: timeout}
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "ipsw-timeline-cli/1.0 (+https://ipsw.me)")

	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("unexpected status %d", resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	return body, nil
}

func parseFeed(data []byte) ([]rawItem, error) {
	var rss rawRSS
	if err := xml.Unmarshal(data, &rss); err != nil {
		return nil, err
	}
	return rss.Channel.Items, nil
}

func normalizeItem(r rawItem) Item {
	pub := parsePubDate(r.PubDate)
	title := strings.TrimSpace(r.Title)
	title = cleanReleaseSuffix(title)

	mainPart, device := splitDevice(title)
	basePart, build := splitBuild(mainPart)
	platformLabel, version := splitPlatformVersion(basePart)
	platformKey := platformKeyForTitle(platformLabel)
	canonicalLabel := platformLabelForKey(platformKey)
	prerelease := detectPreRelease(title)

	plainDesc := stripTags(r.Description)
	plainDesc = html.UnescapeString(plainDesc)
	plainDesc = normalizeSpace(plainDesc)
	notes := notesFromDescription(plainDesc)

	device = strings.TrimSpace(device)
	deviceOrNotes := combineDeviceAndNotes(device, notes)

	return Item{
		Title:          r.Title,
		Link:           r.Link,
		PubDate:        pub,
		GUID:           r.GUID,
		Description:    plainDesc,
		PlatformKey:    platformKey,
		PlatformLabel:  canonicalLabel,
		Version:        version,
		Build:          build,
		DeviceOrNotes:  deviceOrNotes,
		PreRelease:     prerelease,
		RawDevice:      device,
		Notes:          notes,
		DisplayDate:    pub.UTC().Format("2006-01-02 15:04 UTC"),
		DisplayVersion: buildVersion(version, build),
	}
}

func buildVersion(version, build string) string {
	version = strings.TrimSpace(version)
	build = strings.TrimSpace(build)
	if version == "" {
		return strings.TrimSpace(build)
	}
	if build == "" {
		return version
	}
	return fmt.Sprintf("%s (%s)", version, build)
}

func cleanReleaseSuffix(title string) string {
	t := strings.TrimSpace(title)
	lower := strings.ToLower(t)
	suffixes := []string{
		" has been released.",
		" has been released",
		" released.",
		" released",
	}
	for _, s := range suffixes {
		if strings.HasSuffix(lower, s) {
			t = strings.TrimSpace(t[:len(t)-len(s)])
			break
		}
	}
	return t
}

func splitDevice(title string) (main, device string) {
	lower := strings.ToLower(title)
	idx := strings.Index(lower, " for ")
	if idx >= 0 {
		return strings.TrimSpace(title[:idx]), strings.TrimSpace(title[idx+len(" for "):])
	}
	return strings.TrimSpace(title), ""
}

func splitBuild(main string) (base, build string) {
	main = strings.TrimSpace(main)
	if strings.HasSuffix(main, ")") {
		start := strings.LastIndex(main, "(")
		if start > 0 && start < len(main)-1 {
			content := strings.TrimSpace(main[start+1 : len(main)-1])
			if content != "" && !strings.Contains(content, "(") && !strings.Contains(content, ")") {
				build = content
				base = strings.TrimSpace(strings.TrimSpace(main[:start]))
				return base, build
			}
		}
	}
	return main, ""
}

func splitPlatformVersion(base string) (platform, version string) {
	parts := strings.Fields(base)
	if len(parts) == 0 {
		return "Other", ""
	}
	platform = parts[0]
	if len(parts) > 1 {
		version = strings.Join(parts[1:], " ")
	}
	return platform, version
}

func detectPreRelease(title string) bool {
	lower := strings.ToLower(title)
	if strings.Contains(lower, "beta") {
		return true
	}
	if strings.Contains(lower, "rc") {
		return true
	}
	if strings.Contains(lower, "release candidate") {
		return true
	}
	return false
}

func parsePubDate(s string) time.Time {
	layouts := []string{
		time.RFC1123Z,
		time.RFC1123,
		time.RFC850,
		time.ANSIC,
		"Mon, 2 Jan 2006 15:04:05 -0700",
	}
	for _, layout := range layouts {
		if t, err := time.Parse(layout, s); err == nil {
			return t
		}
	}
	return time.Unix(0, 0)
}

func stripTags(s string) string {
	var b strings.Builder
	inTag := false
	for _, r := range s {
		switch r {
		case '<':
			inTag = true
		case '>':
			inTag = false
		default:
			if !inTag {
				b.WriteRune(r)
			}
		}
	}
	return b.String()
}

func notesFromDescription(desc string) string {
	lower := strings.ToLower(desc)
	phrase := "has been released"
	idx := strings.Index(lower, phrase)
	if idx < 0 {
		return ""
	}
	after := strings.TrimSpace(desc[idx+len(phrase):])
	return normalizeSpace(after)
}

func normalizeSpace(s string) string {
	fields := strings.Fields(s)
	return strings.Join(fields, " ")
}

func combineDeviceAndNotes(device, notes string) string {
	device = normalizeSpace(device)
	notes = normalizeSpace(notes)

	if device == "" && notes == "" {
		return ""
	}
	if device == "" {
		return notes
	}
	if len(device) < 4 && notes != "" {
		return notes
	}
	if notes == "" {
		return device
	}
	return device + " - " + notes
}

func platformKeyForTitle(platform string) string {
	base := strings.ReplaceAll(strings.ToLower(platform), " ", "")
	switch base {
	case "ios", "iphone":
		return "ios"
	case "ipados", "ipad":
		return "ipados"
	case "macos", "mac":
		return "macos"
	case "watchos", "watch":
		return "watchos"
	case "tvos", "audioos", "homepod", "appletv":
		return "tvos"
	case "visionos", "vision":
		return "visionos"
	default:
		return "other"
	}
}

func platformLabelForKey(key string) string {
	switch key {
	case "ios":
		return "iOS"
	case "ipados":
		return "iPadOS"
	case "macos":
		return "macOS"
	case "watchos":
		return "watchOS"
	case "tvos":
		return "tvOS"
	case "visionos":
		return "visionOS"
	default:
		return "Other"
	}
}

func filterItems(items []Item, contains string) []Item {
	if strings.TrimSpace(contains) == "" {
		return items
	}
	needle := strings.ToLower(contains)
	var out []Item
	for _, it := range items {
		if strings.Contains(strings.ToLower(it.Title), needle) {
			out = append(out, it)
		}
	}
	return out
}

func shouldEnableColor(mode string) bool {
	switch mode {
	case "always":
		return true
	case "never":
		return false
	default:
		if os.Getenv("NO_COLOR") != "" {
			return false
		}
		return isTTY()
	}
}

func isTTY() bool {
	info, err := os.Stdout.Stat()
	if err != nil {
		return false
	}
	return (info.Mode() & os.ModeCharDevice) != 0
}

func renderTable(items []Item, enableColor bool, out io.Writer) {
	totalWidth := terminalWidth()
	indent := 2
	dateWidth := 20
	stripeWidth := 1
	platformWidth := 12
	versionWidth := 24
	gap := 2
	deviceWidth := totalWidth - (indent + dateWidth + 1 + stripeWidth + 1 + platformWidth + 1 + versionWidth + gap)
	if deviceWidth < 16 {
		deviceWidth = 16
	}

	color := colorizer{enabled: enableColor}

	header := buildHeader(deviceWidth)
	fmt.Fprintln(out, header)
	fmt.Fprintln(out, strings.Repeat("-", len(header)))

	var lastDate string
	for _, it := range items {
		day := it.PubDate.UTC().Format("2006-01-02")
		if day != lastDate {
			lastDate = day
			line := dayDivider(day, totalWidth)
			fmt.Fprintln(out, line)
		}

		dateField := pad(truncate(it.DisplayDate, dateWidth), dateWidth)
		stripe := stripeChar(it.PlatformKey)
		platformKey := it.PlatformKey
		if platformKey == "" {
			platformKey = "other"
		}
		plabel := platformLabelForKey(platformKey)
		platformField := pad(truncate(plabel, platformWidth), platformWidth)
		versionText := buildVersion(it.Version, it.Build)
		versionField := pad(truncate(versionText, versionWidth), versionWidth)
		deviceField := pad(truncate(it.DeviceOrNotes, deviceWidth), deviceWidth)

		stripeColored := stripe
		platformColored := platformField
		versionColored := versionField
		deviceColored := deviceField

		colorCode := platformColor(platformKey)
		if enableColor {
			stripeColored = color.color(colorCode, stripe)
			platformColored = color.color(colorCode, platformField)
			versionColored = colorizeVersion(versionField, colorCode, it.PreRelease, color)
			deviceColored = color.dim(deviceField)
		}

		fmt.Fprintf(out, "%s%s %s %s %s  %s\n",
			strings.Repeat(" ", indent),
			dateField,
			stripeColored,
			platformColored,
			versionColored,
			deviceColored,
		)
	}
}

func buildHeader(deviceWidth int) string {
	date := pad("Published", 20)
	stripe := " "
	platform := pad("Platform", 12)
	version := pad("Version (Build)", 24)
	device := pad("Device / Notes", deviceWidth)
	return fmt.Sprintf("  %s %s %s %s  %s", date, stripe, platform, version, device)
}

func dayDivider(day string, totalWidth int) string {
	prefix := " " + day + " "
	dashes := totalWidth - len(prefix)
	if dashes < 0 {
		dashes = 0
	}
	return prefix + strings.Repeat("-", dashes)
}

func platformColor(key string) string {
	switch key {
	case "ios":
		return "31"
	case "ipados":
		return "36"
	case "macos":
		return "32"
	case "watchos", "tvos", "visionos", "other":
		return "35"
	default:
		return "35"
	}
}

func colorizeVersion(version string, colorCode string, prerelease bool, c colorizer) string {
	if version == "" || !c.enabled {
		return version
	}

	if prerelease {
		return c.wrap("1;"+colorCode, version)
	}

	var b strings.Builder
	b.WriteString("\033[" + colorCode + "m")
	for _, r := range version {
		if r >= '0' && r <= '9' {
			b.WriteString("\033[1m")
			b.WriteRune(r)
			b.WriteString("\033[" + colorCode + "m")
		} else {
			b.WriteRune(r)
		}
	}
	b.WriteString("\033[0m")
	return b.String()
}

func stripeChar(platformKey string) string {
	if platformKey == "" {
		platformKey = "other"
	}
	return "▌"
}

func pad(s string, width int) string {
	runes := []rune(s)
	if len(runes) >= width {
		return s
	}
	return s + strings.Repeat(" ", width-len(runes))
}

func truncate(s string, width int) string {
	runes := []rune(s)
	if len(runes) <= width {
		return s
	}
	return string(runes[:width])
}

func terminalWidth() int {
	if cols := os.Getenv("COLUMNS"); cols != "" {
		if n, err := strconv.Atoi(cols); err == nil && n > 0 {
			return n
		}
	}
	return 100
}
