package tui

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"unicode"

	"github.com/atotto/clipboard"
	"github.com/charmbracelet/bubbles/textinput"
)

type credentialsModel struct {
	accessKeyID string
	secretKey   string
	username    string
	saved       bool
	savePath    string
	saveError   string
	revealed    bool
	captured    bool
	confirmExit bool
	editingPath bool
	pathInput   textinput.Model
	copyText    func(string) error
	message     string
	returnKeys  bool
}

func newCredentials(username, keyID, secret string) credentialsModel {
	input := textinput.New()
	input.CharLimit = 4096
	path, err := credentialPath(username, keyID)
	input.SetValue(path)
	c := credentialsModel{username: username, accessKeyID: keyID, secretKey: secret, pathInput: input, savePath: path, copyText: clipboard.WriteAll}
	if err != nil {
		c.saveError = err.Error()
	}
	return c
}

func credentialPath(username, keyID string) (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("could not resolve home directory: %w", err)
	}
	// AWS names cannot contain path separators. Replace them defensively for imports and tests.
	safe := func(value string) string {
		return strings.Map(func(r rune) rune {
			if unicode.IsLetter(r) || unicode.IsDigit(r) || r == '-' || r == '_' {
				return r
			}
			return '_'
		}, value)
	}
	return filepath.Join(home, fmt.Sprintf("%s-%s-credentials.json", safe(username), safe(keyID))), nil
}

func saveCredentialsAt(path, username, keyID, secret string) error {
	data, err := json.MarshalIndent(map[string]string{"username": username, "access_key_id": keyID, "secret_access_key": secret}, "", "  ")
	if err != nil {
		return fmt.Errorf("could not encode credentials: %w", err)
	}
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return fmt.Errorf("could not save credentials; choose a new path: %w", err)
	}
	if _, err = file.Write(data); err != nil {
		_ = file.Close()
		_ = os.Remove(path)
		return fmt.Errorf("could not write credentials: %w", err)
	}
	if err = file.Close(); err != nil {
		_ = os.Remove(path)
		return fmt.Errorf("could not close credentials file: %w", err)
	}
	return nil
}

func saveCredentialsToFile(username, keyID, secret string) (string, error) {
	path, err := credentialPath(username, keyID)
	if err != nil {
		return "", err
	}
	if err = saveCredentialsAt(path, username, keyID, secret); err != nil {
		return "", err
	}
	return path, nil
}
