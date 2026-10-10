// Package model holds Hoshiro's shared data types. Field names and JSON tags
// keep the camelCase wire format of the original Tauri app, so settings.json
// and the database written by earlier versions load unchanged.
package model

type Mode string

const (
	ModeDictation   Mode = "dictation"
	ModeTranslation Mode = "translation"
	ModeAsk         Mode = "ask"
)

func ParseMode(s string) Mode {
	switch s {
	case "translation":
		return ModeTranslation
	case "ask":
		return ModeAsk
	}
	return ModeDictation
}

type ShortcutBindings struct {
	Dictation           []string `json:"dictation"`
	AskAnything         []string `json:"askAnything"`
	Translation         []string `json:"translation"`
	PasteLastTranscript []string `json:"pasteLastTranscript"`
}

func DefaultShortcuts() ShortcutBindings {
	return ShortcutBindings{
		Dictation:           []string{"Fn"},
		AskAnything:         []string{"Fn+Space"},
		Translation:         []string{"Fn+LeftShift"},
		PasteLastTranscript: []string{"Ctrl+Cmd+V"},
	}
}

type MimoAsrConfig struct {
	APIKey  string `json:"apiKey"`
	BaseURL string `json:"baseUrl"`
	Model   string `json:"model"`
}

type CloudflareAsrConfig struct {
	AccountID string `json:"accountId"`
	APIKey    string `json:"apiKey"`
	Model     string `json:"model"`
}

type AzureAsrConfig struct {
	APIKey string `json:"apiKey"`
	Region string `json:"region"`
}

type GoogleAsrConfig struct {
	APIKey             string `json:"apiKey"`
	ServiceAccountJSON string `json:"serviceAccountJson"`
	ProjectID          string `json:"projectId"`
	Location           string `json:"location"`
	Model              string `json:"model"`
}

type AsrConfig struct {
	Provider   string              `json:"provider"` // mimo | cloudflare | azure | google
	Mimo       MimoAsrConfig       `json:"mimo"`
	Cloudflare CloudflareAsrConfig `json:"cloudflare"`
	Azure      AzureAsrConfig      `json:"azure"`
	Google     GoogleAsrConfig     `json:"google"`
}

func blank(s string) bool {
	for _, r := range s {
		if r != ' ' && r != '\t' && r != '\n' && r != '\r' {
			return false
		}
	}
	return true
}

// Ready reports whether the selected speech provider has its credentials.
func (a AsrConfig) Ready() bool {
	switch a.Provider {
	case "mimo":
		return !blank(a.Mimo.APIKey)
	case "cloudflare":
		return !blank(a.Cloudflare.APIKey) && !blank(a.Cloudflare.AccountID)
	case "azure":
		return !blank(a.Azure.APIKey) && !blank(a.Azure.Region)
	case "google":
		return !blank(a.Google.ProjectID) && (!blank(a.Google.APIKey) || !blank(a.Google.ServiceAccountJSON))
	}
	return false
}

// Streaming reports whether the provider transcribes while the user speaks.
func (a AsrConfig) Streaming() bool { return a.Provider == "azure" || a.Provider == "google" }

type LlmProvider string

const (
	LlmGemini    LlmProvider = "gemini"
	LlmAnthropic LlmProvider = "anthropic"
	LlmOpenAI    LlmProvider = "openai"
)

type LlmEndpointConfig struct {
	APIKey string `json:"apiKey"`
	// Empty = official default endpoint.
	BaseURL string `json:"baseUrl"`
	Model   string `json:"model"`
}

type PersonalizationLlm struct {
	FollowRewrite   bool        `json:"followRewrite"`
	Provider        LlmProvider `json:"provider"`
	Model           string      `json:"model"`
	ReasoningEffort string      `json:"reasoningEffort"`
}

type LlmConfig struct {
	Enabled         bool               `json:"enabled"`
	Provider        LlmProvider        `json:"provider"`
	Gemini          LlmEndpointConfig  `json:"gemini"`
	Anthropic       LlmEndpointConfig  `json:"anthropic"`
	OpenAI          LlmEndpointConfig  `json:"openai"`
	TimeoutSecs     uint32             `json:"timeoutSecs"`
	ReasoningEffort string             `json:"reasoningEffort"` // off | low | medium | high
	Personalization PersonalizationLlm `json:"personalization"`
}

func (l *LlmConfig) Endpoint(p LlmProvider) *LlmEndpointConfig {
	switch p {
	case LlmAnthropic:
		return &l.Anthropic
	case LlmOpenAI:
		return &l.OpenAI
	}
	return &l.Gemini
}

func (l *LlmConfig) ActiveEndpoint() *LlmEndpointConfig { return l.Endpoint(l.Provider) }

type StyleConfig struct {
	CustomInstructions string `json:"customInstructions"`
	SmartFormatting    bool   `json:"smartFormatting"`
	AutoAddDictionary  bool   `json:"autoAddDictionary"`
	AIPersonalization  bool   `json:"aiPersonalization"`
	LongFormStyle      string `json:"longFormStyle"` // paragraphs | list
}

type ProfileCorrection struct {
	Wrong string `json:"wrong"`
	Right string `json:"right"`
}

type PersonalProfile struct {
	UpdatedAt            *string             `json:"updatedAt"`
	AnalyzedSessions     uint32              `json:"analyzedSessions"`
	PendingSessions      uint32              `json:"pendingSessions"`
	NextRunAt            *string             `json:"nextRunAt"`
	VerbalTics           []string            `json:"verbalTics"`
	Corrections          []ProfileCorrection `json:"corrections"`
	Vocabulary           []string            `json:"vocabulary"`
	StyleNotes           []string            `json:"styleNotes"`
	PromptAddendum       string              `json:"promptAddendum"`
	AddendumEditedByUser bool                `json:"addendumEditedByUser"`
	LastError            *string             `json:"lastError"`
	Running              bool                `json:"running"`
}

type Settings struct {
	Shortcuts           ShortcutBindings `json:"shortcuts"`
	MicrophoneDeviceID  *string          `json:"microphoneDeviceId"`
	SelectedLanguages   []string         `json:"selectedLanguages"`
	AutoSelectLanguages bool             `json:"autoSelectLanguages"`
	TranslationTargets  []string         `json:"translationTargets"`
	LaunchAtLogin       bool             `json:"launchAtLogin"`
	ShowBarOnDesktop    bool             `json:"showBarOnDesktop"`
	InteractionSounds   bool             `json:"interactionSounds"`
	ShowInDock          bool             `json:"showInDock"`
	MuteWhileDictating  bool             `json:"muteWhileDictating"`
	HistoryRetention    string           `json:"historyRetention"`  // never | 24h | 1w | 1m | 1y | forever
	Appearance          string           `json:"appearance"`        // system | light | dark
	InterfaceLanguage   string           `json:"interfaceLanguage"` // zh-CN | en-US
	Asr                 AsrConfig        `json:"asr"`
	Llm                 LlmConfig        `json:"llm"`
	Style               StyleConfig      `json:"style"`
	OnboardingCompleted bool             `json:"onboardingCompleted"`
}

func DefaultSettings() Settings {
	return Settings{
		Shortcuts:          DefaultShortcuts(),
		SelectedLanguages:  []string{"zh-CN"},
		TranslationTargets: []string{"en-US"},
		LaunchAtLogin:      true,
		InteractionSounds:  true,
		ShowInDock:         true,
		MuteWhileDictating: true,
		HistoryRetention:   "1m",
		Appearance:         "system",
		InterfaceLanguage:  "zh-CN",
		Asr: AsrConfig{
			Provider:   "mimo",
			Mimo:       MimoAsrConfig{BaseURL: "https://api.xiaomimimo.com/v1", Model: "mimo-v2.5-asr"},
			Cloudflare: CloudflareAsrConfig{Model: "@cf/openai/whisper-large-v3-turbo"},
			Azure:      AzureAsrConfig{Region: "eastasia"},
			Google:     GoogleAsrConfig{Location: "global", Model: "chirp_3"},
		},
		Llm: LlmConfig{
			Enabled:         true,
			Provider:        LlmGemini,
			Gemini:          LlmEndpointConfig{Model: "gemini-flash-latest"},
			Anthropic:       LlmEndpointConfig{Model: "claude-haiku-5-5"},
			OpenAI:          LlmEndpointConfig{BaseURL: "https://api.openai.com/v1", Model: "gpt-4.1-mini"},
			TimeoutSecs:     12,
			ReasoningEffort: "off",
			Personalization: PersonalizationLlm{FollowRewrite: true, Provider: LlmGemini, ReasoningEffort: "medium"},
		},
		Style: StyleConfig{SmartFormatting: true, AutoAddDictionary: true, AIPersonalization: true, LongFormStyle: "paragraphs"},
	}
}

type HistoryItem struct {
	ID           string  `json:"id"`
	CreatedAt    string  `json:"createdAt"`
	Mode         Mode    `json:"mode"`
	Status       string  `json:"status"` // completed | dismissed | error | empty | processing
	RawText      *string `json:"rawText"`
	RefinedText  *string `json:"refinedText"`
	SelectedText *string `json:"selectedText"`
	DurationSecs float64 `json:"durationSecs"`
	AppName      *string `json:"appName"`
	BundleID     *string `json:"bundleId"`
	WindowTitle  *string `json:"windowTitle"`
	ErrorMessage *string `json:"errorMessage"`
	HasAudio     bool    `json:"hasAudio"`
	Source       string  `json:"source"`
}

type HistoryQuery struct {
	Mode   *Mode  `json:"mode"`
	Limit  uint32 `json:"limit"`
	Offset uint32 `json:"offset"`
	Search string `json:"search"`
}

type DictionaryWord struct {
	ID        string `json:"id"`
	Word      string `json:"word"`
	Source    string `json:"source"` // manual | auto
	CreatedAt string `json:"createdAt"`
}

type HeatmapDay struct {
	Date     string `json:"date"`
	Words    uint64 `json:"words"`
	Sessions uint64 `json:"sessions"`
}

type Stats struct {
	TotalWords         uint64       `json:"totalWords"`
	TotalDictationSecs float64      `json:"totalDictationSecs"`
	AverageWPM         float64      `json:"averageWpm"`
	TimeSavedSecs      float64      `json:"timeSavedSecs"`
	ActiveDays         uint32       `json:"activeDays"`
	CurrentStreak      uint32       `json:"currentStreak"`
	LongestStreak      uint32       `json:"longestStreak"`
	Heatmap            []HeatmapDay `json:"heatmap"`
}

type MicDevice struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	IsDefault bool   `json:"isDefault"`
	Kind      string `json:"kind"` // builtin | bluetooth | usb | virtual | unknown
}

type PermissionState string

const (
	PermGranted       PermissionState = "granted"
	PermDenied        PermissionState = "denied"
	PermNotDetermined PermissionState = "notDetermined"
)

type Permissions struct {
	Microphone      PermissionState `json:"microphone"`
	Accessibility   PermissionState `json:"accessibility"`
	InputMonitoring PermissionState `json:"inputMonitoring"`
}

type PermissionKind string

const (
	PermMicrophone      PermissionKind = "microphone"
	PermAccessibility   PermissionKind = "accessibility"
	PermInputMonitoring PermissionKind = "inputMonitoring"
)

type TypelessDetection struct {
	Installed    bool              `json:"installed"`
	Running      bool              `json:"running"`
	DataFound    bool              `json:"dataFound"`
	HistoryCount uint32            `json:"historyCount"`
	Shortcuts    *ShortcutBindings `json:"shortcuts"`
}

type TypelessImportOptions struct {
	Settings bool `json:"settings"`
	History  bool `json:"history"`
}

type TypelessImportResult struct {
	SettingsImported bool     `json:"settingsImported"`
	HistoryImported  uint32   `json:"historyImported"`
	HistorySkipped   uint32   `json:"historySkipped"`
	Messages         []string `json:"messages"`
}

type TestResult struct {
	OK        bool   `json:"ok"`
	Message   string `json:"message"`
	LatencyMs uint64 `json:"latencyMs"`
}

// ── Floating bar ──

type BarPhase string

const (
	BarHidden     BarPhase = "hidden"
	BarIdle       BarPhase = "idle"
	BarRecording  BarPhase = "recording"
	BarProcessing BarPhase = "processing"
)

type BarState struct {
	Phase                  BarPhase `json:"phase"`
	Mode                   Mode     `json:"mode"`
	Locked                 bool     `json:"locked"`
	TranslationTargetLabel *string  `json:"translationTargetLabel"`
	ElapsedSecs            float64  `json:"elapsedSecs"`
	ShowIdleOnDesktop      bool     `json:"showIdleOnDesktop"`
	DictationKeyLabel      string   `json:"dictationKeyLabel"`
}

type AlertAction struct {
	ID    string `json:"id"`
	Label string `json:"label"`
	Style string `json:"style"` // primary | secondary | danger
}

type BarAlert struct {
	ID          string        `json:"id"`
	Kind        string        `json:"kind"`
	Title       string        `json:"title"`
	Description *string       `json:"description"`
	Actions     []AlertAction `json:"actions"`
	DurationMs  *uint64       `json:"durationMs"`
	Tone        string        `json:"tone"` // info | warning | error | success
}

type AskAnswer struct {
	ID       string `json:"id"`
	Question string `json:"question"`
	Answer   string `json:"answer"`
}

type UpdateInfo struct {
	Available      bool   `json:"available"`
	CurrentVersion string `json:"currentVersion"`
	Version        string `json:"version,omitempty"`
	Notes          string `json:"notes,omitempty"`
	Date           string `json:"date,omitempty"`
}

type UsageBucket struct {
	Label              string  `json:"label"`
	Sessions           uint64  `json:"sessions"`
	Words              uint64  `json:"words"`
	AudioSecs          float64 `json:"audioSecs"`
	InputTokens        uint64  `json:"inputTokens"`
	OutputTokens       uint64  `json:"outputTokens"`
	Requests           uint64  `json:"requests"`
	UnreportedRequests uint64  `json:"unreportedRequests"`
	Kind               string  `json:"kind"` // asr | llm | ""
	CachedTokens       uint64  `json:"cachedTokens"`
	MedianMs           *uint64 `json:"medianMs"`
	LatencySamples     uint64  `json:"latencySamples"`
	BundleID           *string `json:"bundleId"`
}

type LatencyStat struct {
	Samples  uint64 `json:"samples"`
	MedianMs uint64 `json:"medianMs"`
	P90Ms    uint64 `json:"p90Ms"`
	MeanMs   uint64 `json:"meanMs"`
}

type LatencyReport struct {
	Total LatencyStat `json:"total"`
	Asr   LatencyStat `json:"asr"`
	Llm   LatencyStat `json:"llm"`
}

type OutcomeCounts struct {
	Completed uint64 `json:"completed"`
	Dismissed uint64 `json:"dismissed"`
	Error     uint64 `json:"error"`
	Empty     uint64 `json:"empty"`
}

type UsageStats struct {
	Total                  UsageBucket   `json:"total"`
	CompletedSessions      uint64        `json:"completedSessions"`
	ErrorSessions          uint64        `json:"errorSessions"`
	DictionaryWords        uint64        `json:"dictionaryWords"`
	AutoWords              uint64        `json:"autoWords"`
	Days                   []UsageBucket `json:"days"`
	Modes                  []UsageBucket `json:"modes"`
	Apps                   []UsageBucket `json:"apps"`
	Providers              []UsageBucket `json:"providers"`
	AsrTotal               UsageBucket   `json:"asrTotal"`
	LlmTotal               UsageBucket   `json:"llmTotal"`
	AsrModels              []UsageBucket `json:"asrModels"`
	LlmModels              []UsageBucket `json:"llmModels"`
	Outcomes               OutcomeCounts `json:"outcomes"`
	Latency                LatencyReport `json:"latency"`
	Hours                  []uint64      `json:"hours"`
	Weekdays               []uint64      `json:"weekdays"`
	LongestSessionSecs     float64       `json:"longestSessionSecs"`
	AverageSessionSecs     float64       `json:"averageSessionSecs"`
	AverageWordsPerSession float64       `json:"averageWordsPerSession"`
}

// AppContext describes the app the user is dictating into.
type AppContext struct {
	AppName     *string
	BundleID    *string
	WindowTitle *string
	URL         *string // browser URL when cheaply available
	// SelectedText is the selection in the focused element (via Accessibility).
	SelectedText *string
	// FocusedEditable is true if the focused element is editable, false if
	// nothing editable is focused, nil when unknown.
	FocusedEditable *bool
	// SurroundingText is up to ~1500 chars around the caret.
	SurroundingText *string
	PID             *int32
}
