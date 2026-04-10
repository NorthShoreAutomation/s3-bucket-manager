package tui

type credentialsModel struct {
	accessKeyID string
	secretKey   string
	username    string
	saved       bool
	savePath    string
	saveError   string
}
