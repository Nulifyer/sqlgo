package ui

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"

	"github.com/nulifyer/sqlgo/internal/db"
)

const (
	connectionStepProvider = iota
	connectionStepTarget
	connectionStepAuth
	connectionStepAdvanced
	connectionStepReview
)

type connectionManager struct {
	root            *Root
	layout          *tview.Flex
	list            *tview.List
	formPages       *tview.Pages
	form            *tview.Form
	testStatus      *tview.TextView
	details         *tview.TextView
	profiles        []db.ConnectionProfile
	providers       []db.Provider
	step            int
	draft           db.ConnectionProfile
	password        string
	selectedProfile string
	testing         bool
}

func newConnectionManager(root *Root) *connectionManager {
	m := &connectionManager{
		root:       root,
		list:       tview.NewList(),
		formPages:  tview.NewPages(),
		testStatus: tview.NewTextView(),
		details:    tview.NewTextView(),
		providers:  root.registry.Providers(),
	}

	m.list.SetBorder(true).SetTitle(" Connections ")
	m.list.ShowSecondaryText(false)
	m.list.SetInputCapture(m.handleListKeys)

	m.details.
		SetDynamicColors(true).
		SetWrap(true).
		SetWordWrap(true).
		SetBorder(true).
		SetTitle(" Connection Details ")

	m.testStatus.
		SetDynamicColors(true).
		SetWrap(true).
		SetWordWrap(true).
		SetBorder(true).
		SetTitle(" Test Status ")
	m.testStatus.SetText("[gray]No test run yet.[-]")

	right := tview.NewFlex().
		SetDirection(tview.FlexRow).
		AddItem(m.formPages, 0, 4, true).
		AddItem(m.testStatus, 5, 0, false).
		AddItem(m.details, 0, 2, false)

	m.layout = tview.NewFlex().
		AddItem(m.list, 34, 0, false).
		AddItem(right, 0, 1, true)
	m.layout.SetBorder(true).SetTitle(" Connection Wizard ")

	m.list.SetChangedFunc(func(index int, mainText, secondaryText string, shortcut rune) {
		m.showListSelection(index)
	})
	m.list.SetSelectedFunc(func(index int, mainText, secondaryText string, shortcut rune) {
		if index == 0 {
			m.resetDraft()
			return
		}
		m.loadProfile(index - 1)
	})

	m.resetDraft()
	m.refresh()
	return m
}

func (m *connectionManager) Primitive() tview.Primitive   { return m.layout }
func (m *connectionManager) FocusTarget() tview.Primitive { return m.list }

func (m *connectionManager) handleListKeys(event *tcell.EventKey) *tcell.EventKey {
	switch event.Key() {
	case tcell.KeyTAB:
		m.root.app.SetFocus(m.form)
		return nil
	case tcell.KeyBacktab:
		return nil
	case tcell.KeyEsc:
		m.root.closeOverlay()
		return nil
	case tcell.KeyDEL, tcell.KeyDelete:
		m.deleteProfile()
		return nil
	case tcell.KeyRune:
		switch event.Rune() {
		case 'n':
			m.resetDraft()
			m.root.app.SetFocus(m.form)
			return nil
		case 't':
			if current := m.list.GetCurrentItem(); current > 0 {
				m.loadProfile(current - 1)
				m.root.app.SetFocus(m.form)
			}
			return nil
		}
	}
	return event
}

func (m *connectionManager) handleFormKeys(event *tcell.EventKey) *tcell.EventKey {
	switch event.Key() {
	case tcell.KeyBacktab:
		m.root.app.SetFocus(m.list)
		return nil
	case tcell.KeyEsc:
		m.root.closeOverlay()
		return nil
	case tcell.KeyCtrlT:
		m.testProfile()
		return nil
	case tcell.KeyCtrlS:
		if m.step == connectionStepReview {
			m.saveProfile()
		}
		return nil
	}
	return event
}

func (m *connectionManager) refresh() {
	profiles, err := m.root.store.Load()
	if err != nil {
		m.root.setStatusf("[red]failed to load connections:[-] %v", err)
		return
	}
	m.profiles = profiles
	m.list.Clear()
	m.list.AddItem("+ Create Connection", "Start a new connection wizard", 0, nil)
	for _, profile := range profiles {
		label := fmt.Sprintf("%s [%s]", profile.Name, profile.ProviderID)
		m.list.AddItem(label, summaryLine(profile), 0, nil)
	}

	if len(profiles) == 0 {
		m.list.SetCurrentItem(0)
		m.resetDraft()
		m.details.SetText("No saved connections yet.\n\nUse the wizard:\n1. Provider\n2. Target\n3. Authentication\n4. Advanced\n5. Review\n\nKeybinds: Tab move to form, Shift+Tab back to list, Ctrl+T test, Ctrl+S save on review, Esc close.")
		m.setTestStatus("[gray]No test run yet.[-]")
		return
	}

	index := 1
	for i, profile := range profiles {
		if profile.Name == m.selectedProfile {
			index = i + 1
			break
		}
	}
	m.list.SetCurrentItem(index)
	m.showListSelection(index)
}

func (m *connectionManager) showListSelection(index int) {
	if index <= 0 || index-1 >= len(m.profiles) {
		m.selectedProfile = ""
		m.renderWizardHelp()
		return
	}

	profile := m.profiles[index-1]
	m.selectedProfile = profile.Name
	provider, _ := m.root.registry.Provider(profile.ProviderID)
	m.details.SetText(fmt.Sprintf(
		"[green]%s[-]\n\nProvider: [blue]%s[-]\nAuth: [blue]%s[-]\nTarget: %s\nRead-only: %t\n\nKeys:\nEnter load into wizard\nn create new\nt edit selected\nDel delete\nTab move to form",
		profile.Name,
		provider.DisplayName,
		profile.AuthMode,
		targetSummary(profile),
		profile.ReadOnly,
	))
}

func (m *connectionManager) resetDraft() {
	m.step = connectionStepProvider
	m.password = ""
	m.selectedProfile = ""
	m.testing = false
	m.draft = db.ConnectionProfile{
		ProviderID: m.providers[0].ID,
		AuthMode:   m.providers[0].AuthModes[0],
		Settings: db.ConnectionSettings{
			Port: defaultPort(m.providers[0].ID),
		},
	}
	m.setTestStatus("[gray]No test run yet.[-]")
	m.rebuildForm()
}

func (m *connectionManager) loadProfile(index int) {
	if index < 0 || index >= len(m.profiles) {
		m.resetDraft()
		return
	}
	m.step = connectionStepProvider
	m.draft = m.profiles[index]
	m.password = ""
	m.selectedProfile = m.draft.Name
	m.testing = false
	m.setTestStatus("[gray]Use Ctrl+T or the Test button to validate this connection.[-]")
	m.rebuildForm()
	m.renderWizardHelp()
}

func (m *connectionManager) rebuildForm() {
	form := tview.NewForm()
	form.SetBorder(true)
	form.SetTitle(fmt.Sprintf(" Connection Wizard [%d/5] %s ", m.step+1, stepTitle(m.step)))
	form.SetButtonsAlign(tview.AlignLeft)
	form.SetInputCapture(m.handleFormKeys)

	switch m.step {
	case connectionStepProvider:
		m.buildProviderStep(form)
	case connectionStepTarget:
		m.buildTargetStep(form)
	case connectionStepAuth:
		m.buildAuthStep(form)
	case connectionStepAdvanced:
		m.buildAdvancedStep(form)
	case connectionStepReview:
		m.buildReviewStep(form)
	}

	if m.step > connectionStepProvider {
		form.AddButton("Prev", m.prevStep)
	}
	if m.step < connectionStepReview {
		form.AddButton("Next", m.nextStep)
	}
	if m.step >= connectionStepAuth && m.step < connectionStepReview {
		form.AddButton("Test", m.testProfile)
	}
	if m.step == connectionStepReview {
		form.AddButton("Test", m.testProfile)
		form.AddButton("Save", m.saveProfile)
	}
	form.AddButton("Close", func() { m.root.closeOverlay() })

	m.form = form
	m.formPages.RemovePage("form")
	m.formPages.AddPage("form", form, true, true)
	m.root.app.SetFocus(form)
	m.renderWizardHelp()
}

func (m *connectionManager) buildProviderStep(form *tview.Form) {
	labels := make([]string, 0, len(m.providers))
	current := 0
	for i, provider := range m.providers {
		labels = append(labels, provider.DisplayName)
		if provider.ID == m.draft.ProviderID {
			current = i
		}
	}
	form.AddInputField("Name", m.draft.Name, 40, nil, func(text string) {
		m.draft.Name = strings.TrimSpace(text)
	})
	form.AddDropDown("DB Type", labels, current, func(option string, index int) {
		if index < 0 || index >= len(m.providers) {
			return
		}
		provider := m.providers[index]
		m.draft.ProviderID = provider.ID
		if len(provider.AuthModes) > 0 {
			m.draft.AuthMode = provider.AuthModes[0]
		}
		m.draft.Settings.Port = defaultPort(provider.ID)
		m.renderWizardHelp()
	})
}

func (m *connectionManager) buildTargetStep(form *tview.Form) {
	switch m.draft.ProviderID {
	case db.ProviderSQLite:
		form.AddInputField("File Path", m.draft.Settings.FilePath, 120, nil, func(text string) {
			m.draft.Settings.FilePath = strings.TrimSpace(text)
		})
	case db.ProviderSnowflake:
		form.AddInputField("Account", m.draft.Settings.Account, 80, nil, func(text string) {
			m.draft.Settings.Account = strings.TrimSpace(text)
		})
		form.AddInputField("Database", m.draft.Settings.Database, 60, nil, func(text string) {
			m.draft.Settings.Database = strings.TrimSpace(text)
		})
		form.AddInputField("Schema", m.draft.Settings.Schema, 60, nil, func(text string) {
			m.draft.Settings.Schema = strings.TrimSpace(text)
		})
		form.AddInputField("Warehouse", m.draft.Settings.Warehouse, 60, nil, func(text string) {
			m.draft.Settings.Warehouse = strings.TrimSpace(text)
		})
		form.AddInputField("Role", m.draft.Settings.Role, 60, nil, func(text string) {
			m.draft.Settings.Role = strings.TrimSpace(text)
		})
	default:
		form.AddInputField("Host", m.draft.Settings.Host, 60, nil, func(text string) {
			m.draft.Settings.Host = strings.TrimSpace(text)
		})
		form.AddInputField("Port", intString(m.draft.Settings.Port), 8, nil, func(text string) {
			if port, err := strconv.Atoi(strings.TrimSpace(text)); err == nil {
				m.draft.Settings.Port = port
			}
		})
		form.AddInputField("Database", m.draft.Settings.Database, 60, nil, func(text string) {
			m.draft.Settings.Database = strings.TrimSpace(text)
		})
		form.AddInputField("Schema", m.draft.Settings.Schema, 60, nil, func(text string) {
			m.draft.Settings.Schema = strings.TrimSpace(text)
		})
	}
}

func (m *connectionManager) buildAuthStep(form *tview.Form) {
	provider, _ := m.root.registry.Provider(m.draft.ProviderID)
	options := authModeLabels(provider)
	current := 0
	for i, mode := range provider.AuthModes {
		if mode == m.draft.AuthMode {
			current = i
			break
		}
	}
	form.AddDropDown("Auth Mode", options, current, func(option string, index int) {
		if index >= 0 && index < len(provider.AuthModes) {
			m.draft.AuthMode = provider.AuthModes[index]
			m.renderWizardHelp()
		}
	})

	switch m.draft.AuthMode {
	case db.AuthWindowsSSO, db.AuthSQLiteFile:
		form.AddTextView("Info", "No credentials required for this auth mode.", 0, 2, false, false)
	case db.AuthAzureAD:
		form.AddInputField("Username", m.draft.Settings.Username, 60, nil, func(text string) {
			m.draft.Settings.Username = strings.TrimSpace(text)
		})
		form.AddPasswordField("Password", m.password, 60, '*', func(text string) {
			m.password = text
		})
	default:
		form.AddInputField("Username", m.draft.Settings.Username, 60, nil, func(text string) {
			m.draft.Settings.Username = strings.TrimSpace(text)
		})
		form.AddPasswordField("Password", m.password, 60, '*', func(text string) {
			m.password = text
		})
	}
}

func (m *connectionManager) buildAdvancedStep(form *tview.Form) {
	form.AddCheckbox("Read Only", m.draft.ReadOnly, func(checked bool) {
		m.draft.ReadOnly = checked
	})
	form.AddInputField("Params", m.draft.Settings.AdditionalParams, 120, nil, func(text string) {
		m.draft.Settings.AdditionalParams = strings.TrimSpace(text)
	})
	form.AddInputField("Notes", m.draft.Notes, 120, nil, func(text string) {
		m.draft.Notes = strings.TrimSpace(text)
	})
}

func (m *connectionManager) buildReviewStep(form *tview.Form) {
	form.AddTextView("Summary", reviewSummary(m.draft), 0, 10, false, false)
}

func (m *connectionManager) nextStep() {
	if err := m.validateCurrentStep(); err != nil {
		m.root.setStatusf("[red]wizard blocked:[-] %v", err)
		return
	}
	if m.step < connectionStepReview {
		m.step++
		m.rebuildForm()
	}
}

func (m *connectionManager) prevStep() {
	if m.step > connectionStepProvider {
		m.step--
		m.rebuildForm()
	}
}

func (m *connectionManager) validateCurrentStep() error {
	switch m.step {
	case connectionStepProvider:
		if strings.TrimSpace(m.draft.Name) == "" {
			return fmt.Errorf("connection name is required")
		}
	case connectionStepTarget:
		switch m.draft.ProviderID {
		case db.ProviderSQLite:
			if strings.TrimSpace(m.draft.Settings.FilePath) == "" {
				return fmt.Errorf("sqlite file path is required")
			}
		case db.ProviderSnowflake:
			if strings.TrimSpace(m.draft.Settings.Account) == "" {
				return fmt.Errorf("snowflake account is required")
			}
		default:
			if strings.TrimSpace(m.draft.Settings.Host) == "" {
				return fmt.Errorf("host is required")
			}
		}
	case connectionStepAuth:
		switch m.draft.AuthMode {
		case db.AuthWindowsSSO, db.AuthSQLiteFile:
			return nil
		default:
			if strings.TrimSpace(m.draft.Settings.Username) == "" {
				return fmt.Errorf("username is required")
			}
		}
	}
	return nil
}

func (m *connectionManager) currentProfile() db.ConnectionProfile {
	profile := m.draft
	profile.Settings.PasswordKey = profile.SecretKey()
	return profile
}

func (m *connectionManager) testProfile() {
	if m.testing {
		m.root.setStatusf("[yellow]test already running[-] %s", m.draft.Name)
		return
	}
	profile := m.currentProfile()
	if err := profile.Validate(); err != nil {
		m.setTestStatus(fmt.Sprintf("[red]Validation failed:[-] %v", err))
		m.root.setStatusf("[red]test blocked:[-] %v", err)
		return
	}

	m.testing = true
	m.setTestStatus(fmt.Sprintf("[yellow]Testing connection...[-]\n\nName: %s\nTarget: %s\nAuth: %s", profile.Name, targetSummary(profile), profile.AuthMode))

	go func() {
		err := db.PingWithSecrets(context.Background(), profile, m.root.registry, tempSecretStore{
			base:     m.root.secrets,
			override: map[string]string{profile.SecretKey(): m.password},
		})
		m.root.app.QueueUpdateDraw(func() {
			m.testing = false
			if err != nil {
				m.setTestStatus(fmt.Sprintf("[red]Connection failed.[-]\n\nName: %s\nError: %v", profile.Name, err))
				m.root.setStatusf("[red]connection failed:[-] %v", err)
				return
			}
			m.setTestStatus(fmt.Sprintf("[green]Connection successful.[-]\n\nName: %s\nTarget: %s", profile.Name, targetSummary(profile)))
			m.root.setStatusf("[green]connection ok[-] %s", profile.Name)
		})
	}()
	m.root.setStatusf("[yellow]testing connection[-] %s", profile.Name)
}

func (m *connectionManager) saveProfile() {
	profile := m.currentProfile()
	if err := profile.Validate(); err != nil {
		m.root.setStatusf("[red]save failed:[-] %v", err)
		return
	}
	if strings.TrimSpace(m.password) != "" {
		if err := m.root.secrets.Set(profile.SecretKey(), m.password); err != nil {
			m.root.setStatusf("[red]password save failed:[-] %v", err)
			return
		}
	}
	if err := m.root.store.Save(profile); err != nil {
		m.root.setStatusf("[red]save failed:[-] %v", err)
		return
	}
	m.selectedProfile = profile.Name
	m.root.setStatusf("[green]saved connection[-] %s", profile.Name)
	m.setTestStatus(fmt.Sprintf("[green]Saved connection.[-]\n\nName: %s", profile.Name))
	m.refresh()
}

func (m *connectionManager) deleteProfile() {
	name := m.selectedProfile
	if name == "" {
		if current := m.list.GetCurrentItem(); current > 0 && current-1 < len(m.profiles) {
			name = m.profiles[current-1].Name
		}
	}
	if name == "" {
		m.root.setStatusf("[red]delete blocked:[-] select a saved connection")
		return
	}
	_ = m.root.secrets.Remove("profile:" + strings.ToLower(name) + ":password")
	if err := m.root.store.Delete(name); err != nil {
		m.root.setStatusf("[red]delete failed:[-] %v", err)
		return
	}
	m.root.setStatusf("[green]deleted connection[-] %s", name)
	m.resetDraft()
	m.refresh()
}

func (m *connectionManager) renderWizardHelp() {
	provider, _ := m.root.registry.Provider(m.draft.ProviderID)
	m.details.SetText(fmt.Sprintf(
		"[yellow]Wizard step:[-] %s\n\nProvider: [blue]%s[-]\nAuth: [blue]%s[-]\nTarget: %s\n\nBehavior:\nOnly fields relevant to the selected provider and auth mode are shown.\n\nKeys:\nTab next focus group\nShift+Tab previous focus group\nCtrl+T test connection\nCtrl+S save on review\nEsc close overlay",
		stepTitle(m.step),
		provider.DisplayName,
		m.draft.AuthMode,
		targetSummary(m.draft),
	))
}

func (m *connectionManager) setTestStatus(message string) {
	m.testStatus.SetText(message)
}

func stepTitle(step int) string {
	switch step {
	case connectionStepProvider:
		return "Provider"
	case connectionStepTarget:
		return "Target"
	case connectionStepAuth:
		return "Authentication"
	case connectionStepAdvanced:
		return "Advanced"
	case connectionStepReview:
		return "Review"
	default:
		return "Wizard"
	}
}

func authModeLabels(provider db.Provider) []string {
	labels := make([]string, 0, len(provider.AuthModes))
	for _, mode := range provider.AuthModes {
		labels = append(labels, string(mode))
	}
	return labels
}

func targetSummary(profile db.ConnectionProfile) string {
	switch profile.ProviderID {
	case db.ProviderSQLite:
		return emptyFallback(profile.Settings.FilePath, "(file not set)")
	case db.ProviderSnowflake:
		return emptyFallback(profile.Settings.Account, "(account not set)")
	default:
		host := emptyFallback(profile.Settings.Host, "(host not set)")
		if profile.Settings.Port > 0 {
			return fmt.Sprintf("%s:%d", host, profile.Settings.Port)
		}
		return host
	}
}

func reviewSummary(profile db.ConnectionProfile) string {
	return fmt.Sprintf(
		"Name: %s\nProvider: %s\nAuth: %s\nTarget: %s\nDatabase: %s\nSchema: %s\nRead only: %t\nParams: %s\nNotes: %s\n\nPassword handling: stored in OS keychain when provided.",
		profile.Name,
		profile.ProviderID,
		profile.AuthMode,
		targetSummary(profile),
		emptyFallback(profile.Settings.Database, "(none)"),
		emptyFallback(profile.Settings.Schema, "(none)"),
		profile.ReadOnly,
		emptyFallback(profile.Settings.AdditionalParams, "(none)"),
		emptyFallback(profile.Notes, "(none)"),
	)
}

func defaultPort(provider db.ProviderID) int {
	switch provider {
	case db.ProviderSQLServer, db.ProviderAzureSQL:
		return 1433
	case db.ProviderPostgres:
		return 5432
	case db.ProviderMySQL:
		return 3306
	case db.ProviderSybase:
		return 5000
	default:
		return 0
	}
}

func summaryLine(profile db.ConnectionProfile) string {
	return fmt.Sprintf("%s • %s", profile.AuthMode, targetSummary(profile))
}

func intString(value int) string {
	if value == 0 {
		return ""
	}
	return strconv.Itoa(value)
}

type tempSecretStore struct {
	base     db.SecretStore
	override map[string]string
}

func (s tempSecretStore) Get(key string) (string, error) {
	if value, ok := s.override[key]; ok && value != "" {
		return value, nil
	}
	return s.base.Get(key)
}

func (s tempSecretStore) Set(key, value string) error { return s.base.Set(key, value) }
func (s tempSecretStore) Remove(key string) error     { return s.base.Remove(key) }

func emptyFallback(value, fallback string) string {
	if value == "" {
		return fallback
	}
	return value
}
