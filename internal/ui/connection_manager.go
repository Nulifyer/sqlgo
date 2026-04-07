package ui

import (
	"context"
	"fmt"
	"strings"

	"github.com/rivo/tview"

	"github.com/nulifyer/sqlgo/internal/db"
)

type connectionManager struct {
	root             *Root
	layout           *tview.Flex
	list             *tview.List
	form             *tview.Form
	details          *tview.TextView
	profiles         []db.ConnectionProfile
	selectedProvider db.ProviderID
}

func newConnectionManager(root *Root) *connectionManager {
	m := &connectionManager{
		root:    root,
		list:    tview.NewList(),
		form:    tview.NewForm(),
		details: tview.NewTextView(),
	}

	m.list.SetBorder(true).SetTitle(" Profiles ")
	m.list.ShowSecondaryText(false)

	m.details.
		SetDynamicColors(true).
		SetWrap(true).
		SetWordWrap(true).
		SetBorder(true).
		SetTitle(" Profile Details ")

	m.form.SetBorder(true).SetTitle(" Edit Profile ")
	m.form.SetButtonsAlign(tview.AlignLeft)

	m.buildForm()

	right := tview.NewFlex().
		SetDirection(tview.FlexRow).
		AddItem(m.form, 0, 3, true).
		AddItem(m.details, 0, 2, false)

	m.layout = tview.NewFlex().
		AddItem(m.list, 36, 0, false).
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

func (m *connectionManager) Primitive() tview.Primitive {
	return m.layout
}

func (m *connectionManager) FocusTarget() tview.Primitive {
	return m.form
}

func (m *connectionManager) refresh() {
	profiles, err := m.root.store.Load()
	if err != nil {
		m.root.setStatusf("[red]failed to load profiles:[-] %v", err)
		return
	}

	m.profiles = profiles
	m.list.Clear()
	m.list.AddItem("+ New Profile", "", 0, func() {
		m.resetForm()
	})

	for i, profile := range profiles {
		profileIndex := i
		label := fmt.Sprintf("%s [%s]", profile.Name, profile.ProviderID)
		m.list.AddItem(label, "", 0, func() {
			m.loadProfile(profileIndex)
		})
	}

	if len(profiles) == 0 {
		m.resetForm()
		m.details.SetText("No saved profiles yet.\n\nCreate a profile with a provider and DSN, then use Test to verify connectivity.")
		return
	}

	m.showProfile(0)
}

func (m *connectionManager) showProfile(index int) {
	if index <= 0 || index-1 >= len(m.profiles) {
		m.details.SetText("Create a profile by entering a name, provider, and DSN.\n\nProfiles are saved under your user config directory.")
		return
	}

	profile := m.profiles[index-1]
	provider, _ := m.root.registry.Provider(profile.ProviderID)

	var authModes []string
	for _, mode := range provider.AuthModes {
		authModes = append(authModes, string(mode))
	}

	m.details.SetText(fmt.Sprintf(
		"[green]%s[-]\n\nProvider: [blue]%s[-]\nDriver: [blue]%s[-]\nRead-only: %t\n\nAuth modes: %s\n\nNotes:\n%s\n\nProfile file:\n%s",
		profile.Name,
		provider.DisplayName,
		provider.DriverName,
		profile.ReadOnly,
		strings.Join(authModes, ", "),
		emptyFallback(profile.Notes, "(none)"),
		m.root.store.Path(),
	))
}

func (m *connectionManager) loadProfile(index int) {
	if index <= 0 || index-1 >= len(m.profiles) {
		m.resetForm()
		return
	}

	profile := m.profiles[index-1]
	m.selectedProvider = profile.ProviderID

	nameField := m.form.GetFormItemByLabel("Name").(*tview.InputField)
	nameField.SetText(profile.Name)

	providerField := m.form.GetFormItemByLabel("Provider").(*tview.DropDown)
	providerIndex := 0
	for i, provider := range m.root.registry.Providers() {
		if provider.ID == profile.ProviderID {
			providerIndex = i
			break
		}
	}
	providerField.SetCurrentOption(providerIndex)

	dsnField := m.form.GetFormItemByLabel("DSN").(*tview.InputField)
	dsnField.SetText(profile.DSN)

	notesField := m.form.GetFormItemByLabel("Notes").(*tview.InputField)
	notesField.SetText(profile.Notes)

	readOnlyField := m.form.GetFormItemByLabel("Read only").(*tview.Checkbox)
	readOnlyField.SetChecked(profile.ReadOnly)

	m.showProfile(index)
}

func (m *connectionManager) buildForm() {
	providers := m.root.registry.Providers()
	providerLabels := make([]string, 0, len(providers))
	for _, provider := range providers {
		providerLabels = append(providerLabels, provider.DisplayName)
	}

	m.selectedProvider = providers[0].ID

	m.form.AddInputField("Name", "", 36, nil, nil)
	m.form.AddDropDown("Provider", providerLabels, 0, func(option string, optionIndex int) {
		m.selectedProvider = providers[optionIndex].ID
	})
	m.form.AddInputField("DSN", "", 120, nil, nil)
	m.form.AddInputField("Notes", "", 120, nil, nil)
	m.form.AddCheckbox("Read only", false, nil)
	m.form.AddButton("Save", m.saveProfile)
	m.form.AddButton("Test", m.testProfile)
	m.form.AddButton("Delete", m.deleteProfile)
	m.form.AddButton("Close", func() {
		m.root.closeOverlay()
	})
}

func (m *connectionManager) resetForm() {
	m.selectedProvider = m.root.registry.Providers()[0].ID
	m.form.GetFormItemByLabel("Name").(*tview.InputField).SetText("")
	m.form.GetFormItemByLabel("Provider").(*tview.DropDown).SetCurrentOption(0)
	m.form.GetFormItemByLabel("DSN").(*tview.InputField).SetText("")
	m.form.GetFormItemByLabel("Notes").(*tview.InputField).SetText("")
	m.form.GetFormItemByLabel("Read only").(*tview.Checkbox).SetChecked(false)
	m.details.SetText("Create a profile by entering a name, provider, and DSN.\n\nThe current implementation stores the DSN in the profile file. Secret storage is a planned follow-up.")
}

func (m *connectionManager) currentProfile() db.ConnectionProfile {
	return db.ConnectionProfile{
		Name:       strings.TrimSpace(m.form.GetFormItemByLabel("Name").(*tview.InputField).GetText()),
		ProviderID: m.selectedProvider,
		DSN:        strings.TrimSpace(m.form.GetFormItemByLabel("DSN").(*tview.InputField).GetText()),
		Notes:      strings.TrimSpace(m.form.GetFormItemByLabel("Notes").(*tview.InputField).GetText()),
		ReadOnly:   m.form.GetFormItemByLabel("Read only").(*tview.Checkbox).IsChecked(),
	}
}

func (m *connectionManager) saveProfile() {
	profile := m.currentProfile()
	if err := m.root.store.Save(profile); err != nil {
		m.root.setStatusf("[red]save failed:[-] %v", err)
		return
	}

	m.root.setStatusf("[green]saved profile[-] %s", profile.Name)
	m.refresh()
}

func (m *connectionManager) testProfile() {
	profile := m.currentProfile()
	if err := profile.Validate(); err != nil {
		m.root.setStatusf("[red]test blocked:[-] %v", err)
		return
	}

	go func() {
		err := db.Ping(context.Background(), profile, m.root.registry)
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

	if err := m.root.store.Delete(name); err != nil {
		m.root.setStatusf("[red]delete failed:[-] %v", err)
		return
	}

	m.root.setStatusf("[green]deleted profile[-] %s", name)
	m.refresh()
	m.resetForm()
}

func emptyFallback(value, fallback string) string {
	if value == "" {
		return fallback
	}
	return value
}
