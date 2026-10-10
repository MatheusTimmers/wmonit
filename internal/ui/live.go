package ui

import (
	"context"
	"os"
	"path/filepath"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/fsnotify/fsnotify"

	"github.com/timmers/wmonit/internal/claude"
	"github.com/timmers/wmonit/internal/gitlab"
	"github.com/timmers/wmonit/internal/herdr"
	"github.com/timmers/wmonit/internal/hub"
	"github.com/timmers/wmonit/internal/session"
	"github.com/timmers/wmonit/internal/worktree"
)

const (
	eventsRetry   = 10 * time.Second
	eventDebounce = 150 * time.Millisecond
	metaEvery     = 15 * time.Second
	usageEvery    = 30 * time.Second
)

type watcher interface {
	Watch(ctx context.Context, events []string, onStart func(), onEvent func(herdr.Event)) error
}

type eventsUpMsg struct{}

type eventsDownMsg struct{ err error }

type herdrEventMsg struct{ ev herdr.Event }

type eventPollMsg struct{}

type mrStatusMsg struct {
	statuses map[string]*gitlab.MRStatus
}

type usageMsg struct {
	usage map[string]usageEntry
}

type usageEntry struct {
	usage   claude.Usage
	modTime time.Time
}

type metaDoneMsg struct{ err error }

type liveState struct {
	ch         chan tea.Msg
	eventsOn   bool
	pollQueued bool
	metaAt     time.Time
	metaBusy   bool
	metaDirty  bool
	usageAt    time.Time
	usageBusy  bool
	mrStatus   map[string]*gitlab.MRStatus
	usage      map[string]usageEntry
}

func newLiveState() liveState {
	return liveState{
		ch:       make(chan tea.Msg, 64),
		mrStatus: map[string]*gitlab.MRStatus{},
		usage:    map[string]usageEntry{},
	}
}

func (m Model) startEvents() tea.Cmd {
	if _, ok := m.hr.(watcher); ok && m.cfg.Herdr.EventsOn() {
		go m.restartEvents()
	}
	return m.nextEvent()
}

func (m Model) nextEvent() tea.Cmd {
	ch := m.live.ch
	return func() tea.Msg { return <-ch }
}

func (m Model) updateLive(msg tea.Msg) (tea.Model, tea.Cmd, bool) {
	switch msg := msg.(type) {
	case eventsUpMsg:
		m.live.eventsOn = true
		return m, tea.Batch(m.nextEvent(), m.pollHerdr()), true
	case eventsDownMsg:
		m.live.eventsOn = false
		return m, tea.Batch(m.nextEvent(), tea.Tick(eventsRetry, func(time.Time) tea.Msg { return retryEventsMsg{} })), true
	case retryEventsMsg:
		go m.restartEvents()
		return m, nil, true
	case herdrEventMsg:
		cmds := []tea.Cmd{m.nextEvent()}
		if !m.live.pollQueued {
			m.live.pollQueued = true
			cmds = append(cmds, tea.Tick(eventDebounce, func(time.Time) tea.Msg { return eventPollMsg{} }))
		}
		return m, tea.Batch(cmds...), true
	case eventPollMsg:
		m.live.pollQueued = false
		return m, m.pollHerdr(), true
	case mrStatusMsg:
		for k, v := range msg.statuses {
			m.live.mrStatus[k] = v
		}
		m.live.metaAt, m.live.metaDirty = time.Time{}, true
		return m, m.publishMeta(), true
	case usageMsg:
		m.live.usageBusy = false
		m.live.usage = msg.usage
		return m, nil, true
	case metaDoneMsg:
		m.live.metaBusy = false
		if m.live.metaDirty {
			m.live.metaAt = time.Time{}
			return m, m.publishMeta(), true
		}
		return m, nil, true
	case replyChangedMsg:
		return m, tea.Batch(m.nextEvent(), m.checkHubReply()), true
	}
	return m, nil, false
}

type retryEventsMsg struct{}

func (m Model) restartEvents() {
	w, ok := m.hr.(watcher)
	if !ok {
		return
	}
	ch := m.live.ch
	err := w.Watch(context.Background(), herdr.WatchedEvents,
		func() { ch <- eventsUpMsg{} },
		func(e herdr.Event) {
			select {
			case ch <- herdrEventMsg{ev: e}:
			default:
			}
		})
	ch <- eventsDownMsg{err: err}
}

func (m Model) pollInterval(snap hub.Snapshot) time.Duration {
	switch {
	case !snap.Online():
		return wsPollOffline
	case !m.live.eventsOn || m.sui.hubPending || m.sui.hubWaiting:
		return wsPollEvery
	}
	for _, s := range m.sess.Sessions {
		if s.Active() && s.PendingPrompt != "" {
			return wsPollEvery
		}
	}
	return wsPollEvents
}

func (m Model) sessionMR(s session.Session) (*gitlab.MR, string) {
	mr := hub.FindMR(s, m.allMRs())
	if mr != nil {
		return mr, mr.ShortRef()
	}
	return nil, s.MRRef()
}

type mrStatuser interface {
	MRStatus(ctx context.Context, projectID, iid int) (*gitlab.MRStatus, error)
}

func (m Model) fetchMRStatus() tea.Cmd {
	src, ok := m.glSrc.(mrStatuser)
	if !ok {
		return nil
	}
	want := map[string]gitlab.MR{}
	for _, s := range m.sess.Sessions {
		if !s.Active() {
			continue
		}
		if mr, ref := m.sessionMR(s); mr != nil {
			want[ref] = *mr
		}
	}
	if len(want) == 0 {
		return nil
	}
	ctx := m.fetch.ctx
	return func() tea.Msg {
		out := map[string]*gitlab.MRStatus{}
		for ref, mr := range want {
			if st, err := src.MRStatus(ctx, mr.ProjectID, mr.IID); err == nil {
				out[ref] = st
			}
		}
		return mrStatusMsg{statuses: out}
	}
}

func (m *Model) refreshUsage() tea.Cmd {
	if m.live.usageBusy || time.Since(m.live.usageAt) < usageEvery {
		return nil
	}
	m.live.usageBusy, m.live.usageAt = true, time.Now()
	type job struct{ id, claudeID string }
	var jobs []job
	for _, s := range m.sess.Sessions {
		if s.ClaudeSession != "" {
			jobs = append(jobs, job{s.ID, s.ClaudeSession})
		}
	}
	prev := m.live.usage
	return func() tea.Msg {
		out := map[string]usageEntry{}
		for _, j := range jobs {
			path := claude.TranscriptPath(j.claudeID)
			if path == "" {
				continue
			}
			info, err := os.Stat(path)
			if err != nil {
				continue
			}
			if old, ok := prev[j.id]; ok && old.modTime.Equal(info.ModTime()) {
				out[j.id] = old
				continue
			}
			if u, err := claude.ReadUsage(path); err == nil {
				out[j.id] = usageEntry{usage: u, modTime: info.ModTime()}
			}
		}
		return usageMsg{usage: out}
	}
}

func (m *Model) publishMeta() tea.Cmd {
	if !m.cfg.Herdr.MetadataOn() || m.live.metaBusy || time.Since(m.live.metaAt) < metaEvery || !m.sui.snap.Online() {
		return nil
	}
	m.live.metaBusy, m.live.metaAt, m.live.metaDirty = true, time.Now(), false
	type job struct {
		ws  string
		cur map[string]string
		in  hub.MetaInput
	}
	var jobs []job
	for _, s := range m.sess.Sessions {
		if !s.Active() || s.Workspace == "" {
			continue
		}
		w, ok := m.sui.snap.Workspace(s.Workspace)
		if !ok {
			continue
		}
		mr, ref := m.sessionMR(s)
		jobs = append(jobs, job{ws: w.ID, cur: hub.CurrentTokens(w.Tokens), in: hub.MetaInput{
			Session:    s,
			JiraStatus: m.issueStatus(s.IssueKey()),
			MR:         mr,
			MRStatus:   m.live.mrStatus[ref],
		}})
	}
	h := m.hr
	return func() tea.Msg {
		var firstErr error
		for _, j := range jobs {
			if info, err := worktree.Inspect(hub.Dir(j.in.Session)); err == nil {
				j.in.Git = &info
			}
			diff := hub.TokensChanged(j.cur, hub.Tokens(j.in))
			if len(diff) == 0 {
				continue
			}
			if err := h.ReportMetadata(j.ws, hub.MetaSource, diff); err != nil && firstErr == nil {
				firstErr = err
			}
		}
		return metaDoneMsg{err: firstErr}
	}
}

func (m Model) issueStatus(key string) string {
	if key == "" || m.fetch.ji == nil {
		return ""
	}
	for _, is := range m.fetch.ji.Open {
		if is.Key == key {
			return is.Status
		}
	}
	return ""
}

func (m Model) notify(title, body, sound string) tea.Cmd {
	var cmds []tea.Cmd
	if m.cfg.Herdr.NotifyDesktop() {
		cmds = append(cmds, notifyCmd(title, body))
	}
	if m.cfg.Herdr.NotifyHerdr() {
		h := m.hr
		cmds = append(cmds, func() tea.Msg {
			_ = h.Notify(title, body, sound)
			return nil
		})
	}
	return tea.Batch(cmds...)
}

type replyChangedMsg struct{}

func (m Model) watchHubReply() tea.Cmd {
	dir := m.hubDir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil
	}
	w, err := fsnotify.NewWatcher()
	if err != nil {
		return nil
	}
	if err := w.Add(dir); err != nil {
		w.Close()
		return nil
	}
	ch := m.live.ch
	go func() {
		defer w.Close()
		for {
			select {
			case ev, ok := <-w.Events:
				if !ok {
					return
				}
				if filepath.Base(ev.Name) == claude.ReplyFile && ev.Has(fsnotify.Write|fsnotify.Create|fsnotify.Rename) {
					select {
					case ch <- replyChangedMsg{}:
					default:
					}
				}
			case _, ok := <-w.Errors:
				if !ok {
					return
				}
			}
		}
	}()
	return nil
}

func (m *Model) checkHubReply() tea.Cmd {
	if !m.sui.hubWaiting {
		return nil
	}
	text, at, ok := hub.ReadReply(m.hubDir(), m.sui.hubAsked)
	if !ok {
		return nil
	}
	m.sui.hubWaiting = false
	m.sui.hubReply, m.sui.hubAt = text, at
	if m.mode == modeDetail && m.detail.hub {
		m.detail.loading = false
		m.detail.body = m.hubBody()
	}
	return m.notify("wmonit — pendências", "a análise está pronta", "done")
}
