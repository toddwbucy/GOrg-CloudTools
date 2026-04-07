package tui

import (
	"context"
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/bubbles/v2/textinput"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	awscredentials "github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/toddwbucy/GOrg-CloudTools/internal/cloud/aws/credentials"
)

// fieldIndex identifies which textinput has focus.
type fieldIndex int

const (
	fieldKeyID fieldIndex = iota
	fieldSecret
	fieldToken
	fieldCount
)

// credentialInputModel is the AWS credential entry modal.
// It presents three text fields (Access Key ID, Secret, Session Token)
// and an environment selector (COM / GOV).
type credentialInputModel struct {
	root     *Model
	returnTo Screen

	env    string // "com" or "gov"
	fields [fieldCount]textinput.Model
	focus  fieldIndex

	validating bool
	errMsg     string
}

func newCredentialInputModel(root *Model, returnTo Screen) *credentialInputModel {
	m := &credentialInputModel{
		root:     root,
		returnTo: returnTo,
		env:      "com",
	}

	keyID := textinput.New()
	keyID.Placeholder = "AKIA..."
	keyID.CharLimit = 20

	secret := textinput.New()
	secret.Placeholder = "wJalrXUtnFEMI/..."
	secret.EchoMode = textinput.EchoPassword
	secret.CharLimit = 128

	token := textinput.New()
	token.Placeholder = "AQoD... (optional — leave blank for long-term keys)"
	token.EchoMode = textinput.EchoPassword
	token.CharLimit = 2048

	m.fields[fieldKeyID] = keyID
	m.fields[fieldSecret] = secret
	m.fields[fieldToken] = token

	// Focus the first field.
	_ = m.fields[fieldKeyID].Focus()
	return m
}

func (m *credentialInputModel) Init() tea.Cmd {
	return textinput.Blink
}

func (m *credentialInputModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyPressMsg:
		switch msg.String() {
		case "esc":
			return m, func() tea.Msg { return navigateMsg{screen: m.returnTo} }

		case "tab", "down":
			m.nextField()
		case "shift+tab", "up":
			m.prevField()

		case "ctrl+e", "e":
			// Only toggle env if not inside a text field that might use 'e'
			if m.focus == fieldKeyID && m.fields[fieldKeyID].Value() == "" {
				m.toggleEnv()
			}

		case "enter":
			if m.focus < fieldToken {
				m.nextField()
			} else {
				return m, m.submitCmd()
			}
		}

	case credentialValidationResultMsg:
		m.validating = false
		if msg.err != nil {
			m.errMsg = msg.err.Error()
			return m, nil
		}
		return m, func() tea.Msg {
			return credentialsLoadedMsg{
				provider: "aws",
				env:      m.env,
				ce:       msg.ce,
				returnTo: m.returnTo,
			}
		}
	}

	// Forward to the focused textinput.
	var cmd tea.Cmd
	m.fields[m.focus], cmd = m.fields[m.focus].Update(msg)
	return m, cmd
}

func (m *credentialInputModel) View() tea.View {
	var sb strings.Builder

	envLabel := "COM (us-east-1)"
	if m.env == "gov" {
		envLabel = "GOV (us-gov-west-1)"
	}

	sb.WriteString(titleStyle.Render("AWS Credentials"))
	sb.WriteString("\n\n")
	sb.WriteString(fmt.Sprintf("  Environment: %s  (Tab: next field  Ctrl+E: toggle env  Esc: back)\n\n",
		selectedStyle.Render(envLabel)))

	labels := [fieldCount]string{
		"  Access Key ID",
		"  Secret Access Key",
		"  Session Token",
	}
	for i := fieldIndex(0); i < fieldCount; i++ {
		if fieldIndex(i) == m.focus {
			sb.WriteString(selectedStyle.Render(labels[i]) + "\n")
		} else {
			sb.WriteString(labels[i] + "\n")
		}
		sb.WriteString("  " + m.fields[i].View() + "\n\n")
	}

	if m.validating {
		sb.WriteString(helpStyle.Render("  Validating credentials..."))
	} else if m.errMsg != "" {
		sb.WriteString(errorStyle.Render("  Error: " + m.errMsg))
	} else {
		sb.WriteString(helpStyle.Render("  [Enter] Submit   [Tab] Next field   [Esc] Cancel"))
	}
	sb.WriteString("\n")

	return tea.NewView(sb.String())
}

// nextField advances focus to the next input field.
func (m *credentialInputModel) nextField() {
	m.fields[m.focus].Blur()
	m.focus = (m.focus + 1) % fieldCount
	_ = m.fields[m.focus].Focus()
}

// prevField moves focus to the previous input field.
func (m *credentialInputModel) prevField() {
	m.fields[m.focus].Blur()
	m.focus = (m.focus + fieldCount - 1) % fieldCount
	_ = m.fields[m.focus].Focus()
}

func (m *credentialInputModel) toggleEnv() {
	if m.env == "com" {
		m.env = "gov"
	} else {
		m.env = "com"
	}
}

// credentialValidationResultMsg carries the result of an async STS validation.
type credentialValidationResultMsg struct {
	ce  *CloudEnv
	err error
}

// submitCmd validates the entered credentials asynchronously via STS.
// It runs as a tea.Cmd so it never blocks the event loop.
func (m *credentialInputModel) submitCmd() tea.Cmd {
	keyID := strings.TrimSpace(m.fields[fieldKeyID].Value())
	secret := strings.TrimSpace(m.fields[fieldSecret].Value())
	token := strings.TrimSpace(m.fields[fieldToken].Value())
	env := m.env

	if keyID == "" || secret == "" {
		m.errMsg = "Access Key ID and Secret Access Key are required"
		return nil
	}
	if !credentials.ValidAWSKeyID(keyID) {
		m.errMsg = "Access Key ID format is invalid (must be 20 chars, AKIA/ASIA/AROA prefix)"
		return nil
	}
	if credentials.ContainsXSS(secret) || credentials.ContainsXSS(token) {
		m.errMsg = "Credential fields contain invalid characters"
		return nil
	}

	m.validating = true
	m.errMsg = ""

	return func() tea.Msg {
		region, err := credentials.HomeRegion(env)
		if err != nil {
			return credentialValidationResultMsg{err: fmt.Errorf("unknown environment %q", env)}
		}

		cfg, err := awsconfig.LoadDefaultConfig(context.Background(),
			awsconfig.WithRegion(region),
			awsconfig.WithCredentialsProvider(
				awscredentials.NewStaticCredentialsProvider(keyID, secret, token),
			),
		)
		if err != nil {
			return credentialValidationResultMsg{err: fmt.Errorf("building AWS config: %w", err)}
		}

		identity, err := credentials.Validate(context.Background(), cfg)
		if err != nil {
			return credentialValidationResultMsg{err: err}
		}

		return credentialValidationResultMsg{
			ce: &CloudEnv{
				Cfg:       cfg,
				AccountID: identity.AccountID,
				UserARN:   identity.ARN,
			},
		}
	}
}
