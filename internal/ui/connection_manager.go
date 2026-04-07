package ui

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/rivo/tview"

	"github.com/nulifyer/sqlgo/internal/db"
)

type connectionManager struct {
	root         *Root
	layout       *tview.Flex
	list         *tview.List
	form         *tview.Form
	details      *tview.TextView
	profiles     []db.ConnectionProfile
	providers    []db.Provider
	selectedMode db.AuthMode
}

func newConnectionManager(root *Root) *connectionManager {
	m := &connectionManager{
		root:      root,
		list:      tview.NewList(),
		form:      tview.NewForm(),
		details:   tview.NewTextView(),
		providers: root.registry.Providers(),
	}

	m.list.SetBorder(true).SetTitle(" Connections ")
	m.list.ShowSecondaryText(false)

	m.details.
		SetDynamicColors(true).
		SetWrap(true).
		SetWordWrap(true).
		SetBorder(true).
		SetTitle(" Connection Details ")

	m.form.SetBorder(true).SetTitle(" Create / Edit Connection ")
	m.form.SetButtonsAlign(tview.AlignLeft)
	m.buildForm()

	right := tview.NewFlex().
		SetDirection(tview.FlexRow).
		AddItem(m.form, 0, 3, true).
		AddItem(m.details, 0, 2, false)

	m.layout = tview.NewFlex().
		AddItem(m.list, 34, 0, false).
		AddItem(right, 0, 1, true)
	m.layout.SetBorder(true).SetTitle(" Connection Manager ")

	m.list.SetChangedFunc(func(index int, mainText, secondaryText string, shortcut rune) {
		m.showProfile(index)
	})
	m.list.SetSelectedFunc(func(index int, mainText, secondaryText string, shortcut rune) {
		m.loadProfile(index)
	})

	m.refresh()
	return m
}

func (m *connectionManager) Primitive() tview.Primitive   { return m.layout }
func (m *connectionManager) FocusTarget() tview.Primitive { return m.form }

func (m *connectionManager) buildForm() {
	providerLabels := make([]string, 0, len(m.providers))
	for _, provider := range m.providers {
		providerLabels = append(providerLabels, provider.DisplayName)
	}

	m.form.AddInputField("Name", "", 36, nil, nil)
	m.form.AddDropDown("DB Type", providerLabels, 0, func(option string, optionIndex int) {
		if optionIndex < 0 || optionIndex >= len(m.providers) {
			return
		}
		provider := m.providers[optionIndex]
		m.refreshAuthModes(provider)
		m.renderCreateHelp(provider)
	})
	m.form.AddDropDown("Auth", authModeLabels(m.providers[0]), 0, func(option string, optionIndex int) {
		provider := m.currentProvider()
		if optionIndex < 0 || optionIndex >= len(provider.AuthModes) {
			return
		}
		m.selectedMode = provider.AuthModes[optionIndex]
	})
	m.form.AddInputField("Host", "", 60, nil, nil)
	m.form.AddInputField("Port", "", 8, nil, nil)
	m.form.AddInputField("Database", "", 60, nil, nil)
	m.form.AddInputField("Schema", "", 60, nil, nil)
	m.form.AddInputField("Username", "", 60, nil, nil)
	m.form.AddPasswordField("Password", "", 60, '*', nil)
	m.form.AddInputField("File", "", 120, nil, nil)
	m.form.AddInputField("Account", "", 80, nil, nil)
	m.form.AddInputField("Warehouse", "", 60, nil, nil)
	m.form.AddInputField("Role", "", 60, nil, nil)
	m.form.AddInputField("Params", "", 120, nil, nil)
	m.form.AddInputField("Notes", "", 120, nil, nil)
	m.form.AddCheckbox("Read only", false, nil)
	m.form.AddButton("Test", m.testProfile)
	m.form.AddButton("Save", m.saveProfile)
	m.form.AddButton("Delete", m.deleteProfile)
	m.form.AddButton("Close", func() { m.root.closeOverlay() })

	m.refreshAuthModes(m.providers[0])
	m.selectedMode = m.providers[0].AuthModes[0]
	m.renderCreateHelp(m.providers[0])
}

func (m *connectionManager) refresh() {
	profiles, err := m.root.store.Load()
	if err != nil {
		m.root.setStatusf("[red]failed to load profiles:[-] %v", err)
		return
	}
	m.profiles = profiles
	m.list.Clear()
	m.list.AddItem("+ Create Connection", "", 0, func() { m.resetForm() })
	for i, profile := range profiles {
		idx := i
		m.list.AddItem(fmt.Sprintf("%s [%s]", profile.Name, profile.ProviderID), "", 0, func() {
			m.loadProfile(idx)
		})
	}
	if len(profiles) == 0 {
		m.resetForm()
		m.details.SetText("No saved connections yet.\n\nCreate one now:\n1. Select DB type\n2. Enter connection info\n3. Select auth if applicable\n4. Enter credentials\n5. Test\n6. Save\n\nPasswords are stored in the OS keychain, not in profiles.json.")
		return
	}
	m.showProfile(1)
}

func (m *connectionManager) showProfile(index int) {
	if index <= 0 || index-1 >= len(m.profiles) {
		m.renderCreateHelp(m.currentProvider())
		return
	}
	profile := m.profiles[index-1]
	provider, _ := m.root.registry.Provider(profile.ProviderID)
	m.details.SetText(fmt.Sprintf(
		"[green]%s[-]\n\nProvider: [blue]%s[-]\nAuth: [blue]%s[-]\nRead-only: %t\n\nProfile file:\n%s\n\nPassword storage:\nOS keychain entry %s",
		profile.Name,
		provider.DisplayName,
		profile.AuthMode,
		profile.ReadOnly,
		m.root.store.Path(),
		emptyFallback(profile.Settings.PasswordKey, "(none)"),
	))
}

func (m *connectionManager) loadProfile(index int) {
	if index <= 0 || index-1 >= len(m.profiles) {
		m.resetForm()
		return
	}
	profile := m.profiles[index-1]
	m.setFormValue("Name", profile.Name)
	providerIndex := 0
	for i, provider := range m.providers {
		if provider.ID == profile.ProviderID {
			providerIndex = i
			break
		}
	}
	m.form.GetFormItemByLabel("DB Type").(*tview.DropDown).SetCurrentOption(providerIndex)
	m.refreshAuthModes(m.providers[providerIndex])
	authIndex := 0
	for i, mode := range m.providers[providerIndex].AuthModes {
		if mode == profile.AuthMode {
			authIndex = i
			break
		}
	}
	m.form.GetFormItemByLabel("Auth").(*tview.DropDown).SetCurrentOption(authIndex)
	m.selectedMode = m.providers[providerIndex].AuthModes[authIndex]

	m.setFormValue("Host", profile.Settings.Host)
	m.setFormValue("Port", intString(profile.Settings.Port))
	m.setFormValue("Database", profile.Settings.Database)
	m.setFormValue("Schema", profile.Settings.Schema)
	m.setFormValue("Username", profile.Settings.Username)
	m.setFormValue("Password", "")
	m.setFormValue("File", profile.Settings.FilePath)
	m.setFormValue("Account", profile.Settings.Account)
	m.setFormValue("Warehouse", profile.Settings.Warehouse)
	m.setFormValue("Role", profile.Settings.Role)
	m.setFormValue("Params", profile.Settings.AdditionalParams)
	m.setFormValue("Notes", profile.Notes)
	m.form.GetFormItemByLabel("Read only").(*tview.Checkbox).SetChecked(profile.ReadOnly)
	m.showProfile(index)
}

func (m *connectionManager) resetForm() {
	for _, label := range []string{"Name", "Host", "Port", "Database", "Schema", "Username", "Password", "File", "Account", "Warehouse", "Role", "Params", "Notes"} {
		m.setFormValue(label, "")
	}
	m.form.GetFormItemByLabel("DB Type").(*tview.DropDown).SetCurrentOption(0)
	m.refreshAuthModes(m.providers[0])
	m.form.GetFormItemByLabel("Auth").(*tview.DropDown).SetCurrentOption(0)
	m.selectedMode = m.providers[0].AuthModes[0]
	m.form.GetFormItemByLabel("Read only").(*tview.Checkbox).SetChecked(false)
	m.renderCreateHelp(m.providers[0])
}

func (m *connectionManager) refreshAuthModes(provider db.Provider) {
	item := m.form.GetFormItemByLabel("Auth")
	if item == nil {
		return
	}
	auth := item.(*tview.DropDown)
	auth.SetOptions(authModeLabels(provider), func(option string, optionIndex int) {
		if len(provider.AuthModes) == 0 {
			m.selectedMode = ""
			return
		}
		m.selectedMode = provider.AuthModes[optionIndex]
	})
	if len(provider.AuthModes) > 0 {
		auth.SetCurrentOption(0)
		m.selectedMode = provider.AuthModes[0]
	}
}

func authModeLabels(provider db.Provider) []string {
	labels := make([]string, 0, len(provider.AuthModes))
	for _, mode := range provider.AuthModes {
		labels = append(labels, string(mode))
	}
	return labels
}

func (m *connectionManager) currentProfile() db.ConnectionProfile {
	port, _ := strconv.Atoi(strings.TrimSpace(m.form.GetFormItemByLabel("Port").(*tview.InputField).GetText()))
	profile := db.ConnectionProfile{
		Name:       strings.TrimSpace(m.form.GetFormItemByLabel("Name").(*tview.InputField).GetText()),
		ProviderID: m.currentProvider().ID,
		AuthMode:   m.selectedMode,
		Settings: db.ConnectionSettings{
			Host:             strings.TrimSpace(m.form.GetFormItemByLabel("Host").(*tview.InputField).GetText()),
			Port:             port,
			Database:         strings.TrimSpace(m.form.GetFormItemByLabel("Database").(*tview.InputField).GetText()),
			Schema:           strings.TrimSpace(m.form.GetFormItemByLabel("Schema").(*tview.InputField).GetText()),
			Username:         strings.TrimSpace(m.form.GetFormItemByLabel("Username").(*tview.InputField).GetText()),
			FilePath:         strings.TrimSpace(m.form.GetFormItemByLabel("File").(*tview.InputField).GetText()),
			Account:          strings.TrimSpace(m.form.GetFormItemByLabel("Account").(*tview.InputField).GetText()),
			Warehouse:        strings.TrimSpace(m.form.GetFormItemByLabel("Warehouse").(*tview.InputField).GetText()),
			Role:             strings.TrimSpace(m.form.GetFormItemByLabel("Role").(*tview.InputField).GetText()),
			AdditionalParams: strings.TrimSpace(m.form.GetFormItemByLabel("Params").(*tview.InputField).GetText()),
		},
		ReadOnly: m.form.GetFormItemByLabel("Read only").(*tview.Checkbox).IsChecked(),
		Notes:    strings.TrimSpace(m.form.GetFormItemByLabel("Notes").(*tview.InputField).GetText()),
	}
	profile.Settings.PasswordKey = profile.SecretKey()
	return profile
}

func (m *connectionManager) saveProfile() {
	profile := m.currentProfile()
	if err := profile.Validate(); err != nil {
		m.root.setStatusf("[red]save failed:[-] %v", err)
		return
	}
	if err := m.savePassword(profile); err != nil {
		m.root.setStatusf("[red]password save failed:[-] %v", err)
		return
	}
	if err := m.root.store.Save(profile); err != nil {
		m.root.setStatusf("[red]save failed:[-] %v", err)
		return
	}
	m.root.setStatusf("[green]saved connection[-] %s", profile.Name)
	m.refresh()
}

func (m *connectionManager) testProfile() {
	profile := m.currentProfile()
	if err := profile.Validate(); err != nil {
		m.root.setStatusf("[red]test blocked:[-] %v", err)
		return
	}
	password, err := m.currentPassword()
	if err != nil {
		m.root.setStatusf("[red]password read failed:[-] %v", err)
		return
	}

	go func() {
		err := db.PingWithSecrets(context.Background(), profile, m.root.registry, tempSecretStore{
			base:     m.root.secrets,
			override: map[string]string{profile.SecretKey(): password},
		})
		m.root.app.QueueUpdateDraw(func() {
			if err != nil {
				m.root.setStatusf("[red]connection failed:[-] %v", err)
				return
			}
			m.root.setStatusf("[green]connection ok[-] %s", profile.Name)
		})
	}()
	m.root.setStatusf("[yellow]testing connection[-] %s", profile.Name)
}

func (m *connectionManager) deleteProfile() {
	name := strings.TrimSpace(m.form.GetFormItemByLabel("Name").(*tview.InputField).GetText())
	if name == "" {
		m.root.setStatusf("[red]delete blocked:[-] no profile name selected")
		return
	}
	_ = m.root.secrets.Remove("profile:" + strings.ToLower(name) + ":password")
	if err := m.root.store.Delete(name); err != nil {
		m.root.setStatusf("[red]delete failed:[-] %v", err)
		return
	}
	m.root.setStatusf("[green]deleted connection[-] %s", name)
	m.refresh()
	m.resetForm()
}

func (m *connectionManager) currentProvider() db.Provider {
	idx, _ := m.form.GetFormItemByLabel("DB Type").(*tview.DropDown).GetCurrentOption()
	if idx < 0 || idx >= len(m.providers) {
		return m.providers[0]
	}
	return m.providers[idx]
}

func (m *connectionManager) currentPassword() (string, error) {
	passwordField := strings.TrimSpace(m.form.GetFormItemByLabel("Password").(*tview.InputField).GetText())
	if passwordField != "" {
		return passwordField, nil
	}
	profile := m.currentProfile()
	return m.root.secrets.Get(profile.SecretKey())
}

func (m *connectionManager) savePassword(profile db.ConnectionProfile) error {
	password := strings.TrimSpace(m.form.GetFormItemByLabel("Password").(*tview.InputField).GetText())
	if password == "" {
		return nil
	}
	return m.root.secrets.Set(profile.SecretKey(), password)
}

func (m *connectionManager) setFormValue(label, value string) {
	m.form.GetFormItemByLabel(label).(*tview.InputField).SetText(value)
}

func (m *connectionManager) renderCreateHelp(provider db.Provider) {
	m.details.SetText(fmt.Sprintf(
		"Create connection flow:\n1. Select DB type\n2. Enter server or database info\n3. Select auth\n4. Enter credentials\n5. Test connection\n6. Save connection\n\nCurrent provider: [blue]%s[-]\nSupported auth: %s\n\nPasswords are stored in the OS keychain, not in profiles.json.",
		provider.DisplayName,
		strings.Join(authModeLabels(provider), ", "),
	))
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
	if value, ok := s.override[key]; ok {
		return value, nil
	}
	return s.base.Get(key)
}

func (s tempSecretStore) Set(key, value string) error {
	return s.base.Set(key, value)
}

func (s tempSecretStore) Remove(key string) error {
	return s.base.Remove(key)
}

func emptyFallback(value, fallback string) string {
	if value == "" {
		return fallback
	}
	return value
}
