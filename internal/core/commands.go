package core

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"slices"
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/IcyBlue17/hoshiro/internal/audio"
	"github.com/IcyBlue17/hoshiro/internal/importer"
	"github.com/IcyBlue17/hoshiro/internal/model"
	"github.com/IcyBlue17/hoshiro/internal/personalize"
	"github.com/IcyBlue17/hoshiro/internal/platform"
	"github.com/IcyBlue17/hoshiro/internal/providers"
	"github.com/IcyBlue17/hoshiro/internal/stats"
	"github.com/IcyBlue17/hoshiro/internal/store"
)

// The operations the windows offer (the Tauri version's command surface).

// CompleteOnboarding marks onboarding done.
func (a *App) CompleteOnboarding() error {
	_, err := a.UpdateSettings(func(s *model.Settings) { s.OnboardingCompleted = true })
	return err
}

// ── microphones ──

// ListMics enumerates inputs once access is granted (enumeration blocks on a pending prompt).
func (a *App) ListMics() []model.MicDevice {
	if a.Permissions().Microphone != model.PermGranted {
		return nil
	}
	return platform.ListMics()
}

type levelSink struct {
	a    *App
	gen  int
	last time.Time
}

func (s *levelSink) OnLevel(l float32) {
	if time.Since(s.last) < 28*time.Millisecond {
		return
	}
	s.last = time.Now()
	s.a.micMu.Lock()
	if s.a.micGen == s.gen {
		s.a.micLevel = min(max(l, 0), 1)
	}
	s.a.micMu.Unlock()
	s.a.hub("mic")
}
func (*levelSink) OnChunk(audio.Chunk) {}
func (*levelSink) OnPCM([]int16)       {}
func (*levelSink) OnError(string)      {}

// StartMicTest opens a microphone and reports its level (MicLevel) for up to 2 minutes.
func (a *App) StartMicTest(device string) error {
	a.StopMicTest()
	if device == "default" || device == "auto" {
		device = ""
	}
	rec := audio.NewRecorder(platform.OpenMic)
	a.micMu.Lock()
	a.micGen++
	gen := a.micGen
	a.micMu.Unlock()
	if err := rec.Start(device, &levelSink{a: a, gen: gen}); err != nil {
		return fmt.Errorf("无法打开麦克风：%s", errText(err))
	}
	a.micMu.Lock()
	a.micTest = rec
	a.micMu.Unlock()
	time.AfterFunc(2*time.Minute, func() { // never leave the mic open
		a.micMu.Lock()
		same := a.micGen == gen
		a.micMu.Unlock()
		if same {
			a.StopMicTest()
		}
	})
	return nil
}

func (a *App) StopMicTest() {
	a.micMu.Lock()
	rec := a.micTest
	a.micTest, a.micLevel = nil, 0
	a.micGen++
	a.micMu.Unlock()
	if rec != nil {
		rec.Cancel()
	}
	a.hub("mic")
}

// MicLevel is the mic test's level (0–1) and whether a test runs.
func (a *App) MicLevel() (float32, bool) {
	a.micMu.Lock()
	defer a.micMu.Unlock()
	return a.micLevel, a.micTest != nil
}

// ── shortcut capture ──

func (a *App) StartShortcutCapture() error {
	if a.engine == nil || !a.hotkeysActive() {
		return errors.New("快捷键引擎尚未启动，请先授予「辅助功能」和「输入监控」权限")
	}
	a.captureMu.Lock()
	a.capture.keys, a.capture.done, a.capture.on = "", false, true
	a.captureMu.Unlock()
	a.engine.StartCapture(func(keys string, done bool) {
		a.captureMu.Lock()
		a.capture.keys, a.capture.done = keys, done
		a.captureMu.Unlock()
		a.hub("capture")
	})
	return nil
}

func (a *App) StopShortcutCapture() {
	if a.engine != nil {
		a.engine.StopCapture()
	}
	a.captureMu.Lock()
	a.capture.on = false
	a.captureMu.Unlock()
}

// Capture is the shortcut being recorded: its keys and whether it is complete.
func (a *App) Capture() (keys string, done, active bool) {
	a.captureMu.Lock()
	defer a.captureMu.Unlock()
	return a.capture.keys, a.capture.done, a.capture.on
}

// ── history ──

func (a *App) ListHistory(q model.HistoryQuery) ([]model.HistoryItem, error) {
	rows, err := a.DB.ListHistory(q)
	if err != nil {
		return nil, fmt.Errorf("读取历史记录失败：%w", err)
	}
	out := make([]model.HistoryItem, len(rows))
	for i, r := range rows {
		out[i] = a.DB.ToItem(r)
	}
	return out, nil
}

func (a *App) DeleteHistory(id string) error {
	_, err := a.DB.DeleteHistory(id)
	a.historyChanged()
	return err
}

func (a *App) DeleteAllHistory() error {
	_, err := a.DB.DeleteAllHistory()
	a.historyChanged()
	return err
}

// RetryHistory re-transcribes a saved recording into its row, without inserting anything.
func (a *App) RetryHistory(ctx context.Context, id string) (model.HistoryItem, error) {
	r, err := a.DB.GetHistory(id)
	if err != nil {
		return model.HistoryItem{}, err
	}
	if r == nil {
		return model.HistoryItem{}, errors.New("找不到这条记录")
	}
	path, ok := a.DB.AudioFile(*r)
	if !ok {
		return model.HistoryItem{}, errors.New("没有可用的录音文件，无法重试")
	}
	samples, err := LoadAudio(path)
	if err != nil {
		return model.HistoryItem{}, err
	}
	row := a.process(ctx, processInput{row: *r, ctx: ctxFromRow(*r), settings: a.Settings(), samples: samples, store: true})
	return a.DB.ToItem(row), nil
}

func (a *App) ExportHistoryAudio(id, dst string) error {
	r, err := a.DB.GetHistory(id)
	if err != nil {
		return err
	}
	if r == nil {
		return errors.New("找不到这条记录")
	}
	src, ok := a.DB.AudioFile(*r)
	if !ok {
		return errors.New("这条记录没有保存录音")
	}
	in, err := os.Open(src)
	if err != nil {
		return fmt.Errorf("导出失败：%w", err)
	}
	defer in.Close()
	out, err := os.Create(dst)
	if err != nil {
		return fmt.Errorf("导出失败：%w", err)
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return fmt.Errorf("导出失败：%w", err)
	}
	return out.Close()
}

// ── dictionary ──

// NormalizeWord trims; rejects empty, control characters or > 64 characters.
func NormalizeWord(s string) (string, bool) {
	t := strings.TrimSpace(s)
	if t == "" || len([]rune(t)) > 64 || strings.IndexFunc(t, unicode.IsControl) >= 0 {
		return "", false
	}
	return t, true
}

func dedupeWords(words []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, w := range words {
		if n, ok := NormalizeWord(w); ok && !seen[strings.ToLower(n)] {
			seen[strings.ToLower(n)] = true
			out = append(out, n)
		}
	}
	return out
}

const errWord = "词条不能为空，且长度不能超过 64 个字符"

func (a *App) ListWords() ([]model.DictionaryWord, error) { return a.DB.ListWords() }

func (a *App) AddWords(words []string, source string) ([]model.DictionaryWord, error) {
	if source != "auto" {
		source = "manual"
	}
	words = dedupeWords(words)
	if len(words) == 0 {
		return nil, errors.New(errWord)
	}
	out, err := a.DB.AddWords(words, source)
	a.hub("dictionary")
	a.hub("stats")
	return out, err
}

func (a *App) UpdateWord(id, word string) (model.DictionaryWord, error) {
	w, ok := NormalizeWord(word)
	if !ok {
		return model.DictionaryWord{}, errors.New(errWord)
	}
	out, err := a.DB.UpdateWord(id, w)
	if err != nil {
		if strings.Contains(err.Error(), "UNIQUE") {
			return model.DictionaryWord{}, errors.New("词典中已有这个词")
		}
		return model.DictionaryWord{}, err
	}
	if out == nil {
		return model.DictionaryWord{}, errors.New("找不到这个词条")
	}
	a.hub("dictionary")
	return *out, nil
}

func (a *App) DeleteWords(ids []string) error {
	_, err := a.DB.DeleteWords(ids)
	a.hub("dictionary")
	a.hub("stats")
	return err
}

var csvHeaders = []string{"word", "words", "term", "terms", "vocabulary", "hotword", "词", "词汇", "单词", "词语", "词条", "词典"}

// ParseCSVWords takes the first column of every record, drops an optional header and duplicates.
func ParseCSVWords(text string) []string {
	text = strings.TrimPrefix(text, "\ufeff")
	var records []string
	var field strings.Builder
	firstDone, inQuotes := false, false
	push := func() { records = append(records, field.String()); field.Reset(); firstDone = false }
	r := []rune(text)
	for i := 0; i < len(r); i++ {
		c := r[i]
		if inQuotes {
			if c == '"' {
				if i+1 < len(r) && r[i+1] == '"' {
					i++
					if !firstDone {
						field.WriteRune('"')
					}
				} else {
					inQuotes = false
				}
			} else if !firstDone {
				field.WriteRune(c)
			}
			continue
		}
		switch c {
		case '"':
			if field.Len() == 0 && !firstDone {
				inQuotes = true
			} else if !firstDone {
				field.WriteRune(c)
			}
		case ',', ';', '\t':
			firstDone = true
		case '\r':
		case '\n':
			push()
		default:
			if !firstDone {
				field.WriteRune(c)
			}
		}
	}
	if field.Len() > 0 || firstDone {
		push()
	}
	if len(records) > 0 && slices.Contains(csvHeaders, strings.ToLower(strings.TrimSpace(records[0]))) {
		records = records[1:]
	}
	return dedupeWords(records)
}

func PreviewCSV(path string) ([]string, error) {
	fi, err := os.Stat(path)
	if err != nil {
		return nil, fmt.Errorf("无法读取文件：%w", err)
	}
	if fi.Size() > 5*1024*1024 {
		return nil, errors.New("文件超过 5 MB，无法导入")
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("无法读取文件：%w", err)
	}
	if !utf8Valid(b) {
		return nil, errors.New("文件不是 UTF-8 编码")
	}
	w := ParseCSVWords(string(b))
	if len(w) == 0 {
		return nil, errors.New("没有找到可导入的词条")
	}
	return w, nil
}

// ── stats ──

func (a *App) Stats() (model.Stats, error) {
	rows, err := a.DB.StatRows()
	if err != nil {
		return model.Stats{}, fmt.Errorf("统计失败：%w", err)
	}
	return stats.Compute(rows, time.Now()), nil
}

func (a *App) Usage(days uint32) (model.UsageStats, error) { return a.DB.UsageStats(days) }

// ── providers ──

func (a *App) TestAsr(ctx context.Context, cfg model.AsrConfig) model.TestResult {
	words, _ := a.DB.WordsForPrompt(100)
	return providers.TestAsr(ctx, cfg, a.Settings().SelectedLanguages, words)
}

func (a *App) TestLlm(ctx context.Context, p model.LlmProvider, cfg model.LlmEndpointConfig) model.TestResult {
	return providers.TestLlm(ctx, p, cfg, providers.ParseEffort(a.Settings().Llm.ReasoningEffort))
}

func (a *App) ListModels(ctx context.Context, p model.LlmProvider, cfg model.LlmEndpointConfig) ([]string, error) {
	m, err := providers.ListModels(ctx, p, cfg)
	if err != nil {
		return nil, fmt.Errorf("获取模型列表失败：%w", err)
	}
	return m, nil
}

// ── Typeless migration ──

func (a *App) DetectTypeless() model.TypelessDetection {
	return importer.Detect(len(platform.RunningPIDs(importer.BundleID)) > 0)
}

func (a *App) ImportTypeless(o model.TypelessImportOptions) (model.TypelessImportResult, error) {
	dir := importer.Dir()
	if fi, err := os.Stat(dir); dir == "" || err != nil || !fi.IsDir() {
		return model.TypelessImportResult{}, errors.New("未找到 Typeless 的数据目录")
	}
	res := model.TypelessImportResult{Messages: []string{}}
	if o.Settings {
		if v, err := importer.ReadSettingsJSON(dir); err != nil {
			res.Messages = append(res.Messages, "设置导入失败："+err.Error())
		} else {
			var msgs []string
			if _, err := a.UpdateSettings(func(s *model.Settings) { *s, msgs = importer.MapSettings(v, *s) }); err != nil {
				return res, err
			}
			res.Messages = append(res.Messages, msgs...)
			res.SettingsImported = true
		}
	}
	if o.History {
		if i, s, err := importer.ImportHistory(dir, a.DB); err != nil {
			res.Messages = append(res.Messages, "历史记录导入失败："+err.Error())
		} else {
			res.HistoryImported, res.HistorySkipped = i, s
			a.historyChanged()
		}
	}
	importer.FillMessages(&res, o)
	log.Printf("typeless import: settings=%v history imported=%d skipped=%d", res.SettingsImported, res.HistoryImported, res.HistorySkipped)
	return res, nil
}

func (a *App) QuitTypeless() error {
	if platform.TerminateApp(importer.BundleID) || len(platform.RunningPIDs(importer.BundleID)) == 0 {
		return nil
	}
	return errors.New("无法退出 Typeless，请手动退出")
}

// ── misc ──

func (a *App) CopyText(text string) error {
	if err := platform.CopyToClipboard(text); err != nil {
		return fmt.Errorf("复制失败：%w", err)
	}
	return nil
}

func (a *App) OpenURL(url string) error {
	ok := false
	for _, p := range []string{"https://", "http://", "x-apple.systempreferences:", "mailto:"} {
		ok = ok || strings.HasPrefix(url, p)
	}
	if !ok {
		return errors.New("不支持打开该链接")
	}
	if a.Hooks.OpenURL == nil {
		return errors.New("无法打开链接")
	}
	if err := a.Hooks.OpenURL(url); err != nil {
		return fmt.Errorf("无法打开链接：%w", err)
	}
	return nil
}

// ── personalization ──

func (a *App) Profile() (model.PersonalProfile, error) { return a.Learner.Snapshot() }

func (a *App) RunPersonalization(ctx context.Context) (model.PersonalProfile, error) {
	return a.Learner.Run(ctx, true)
}

func (a *App) UpdateProfile(p model.PersonalProfile) (model.PersonalProfile, error) {
	if !a.Learner.Gate.TryLock() {
		return p, errors.New("请等待当前分析完成")
	}
	old, err := a.DB.Profile()
	if err == nil {
		p.AddendumEditedByUser = p.AddendumEditedByUser || p.PromptAddendum != old.PromptAddendum
		p.UpdatedAt, p.AnalyzedSessions, p.NextRunAt, p.LastError, p.Running = old.UpdatedAt, old.AnalyzedSessions, old.NextRunAt, old.LastError, false
		personalize.Sanitize(&p)
		err = a.DB.SaveProfile(p, nil)
	}
	a.Learner.Gate.Unlock()
	if err != nil {
		return p, err
	}
	a.hub("profile")
	return a.Learner.Snapshot()
}

func (a *App) ResetProfile() (model.PersonalProfile, error) {
	if !a.Learner.Gate.TryLock() {
		return model.PersonalProfile{}, errors.New("请等待当前分析完成")
	}
	err := a.DB.ResetProfile()
	a.Learner.Gate.Unlock()
	if err != nil {
		return model.PersonalProfile{}, err
	}
	a.hub("profile")
	return a.Learner.Snapshot()
}

// ── app icons ──

var iconCache sync.Map // bundle id -> []byte (nil = none)

// AppIcon is an app's icon as PNG (64 px), cached for the process lifetime.
func AppIcon(bundle string) []byte {
	if v, ok := iconCache.Load(bundle); ok {
		return v.([]byte)
	}
	png := platform.AppIconPNG(bundle, 64)
	iconCache.Store(bundle, png)
	return png
}

var _ = store.Now
