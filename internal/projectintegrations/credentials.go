package projectintegrations

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/MiviaLabs/go-mivia/internal/platform/config"
)

var ErrCredentialUnavailable = errors.New("integration credential unavailable")

type Credentials struct {
	Email    string
	APIToken string
}

type CredentialResolver struct {
	LookupEnv func(string) (string, bool)
	ReadFile  func(string) ([]byte, error)
}

func NewCredentialResolver() CredentialResolver {
	return CredentialResolver{}
}

func (resolver CredentialResolver) ResolveAtlassian(refs config.AtlassianCredentialRefs) (Credentials, error) {
	if strings.TrimSpace(refs.CredentialsFile) != "" {
		return resolver.resolveCredentialsFile(refs)
	}
	email, err := resolver.resolveValue("email", refs.EmailEnv, refs.EmailFile)
	if err != nil {
		return Credentials{}, err
	}
	token, err := resolver.resolveValue("api token", refs.APITokenEnv, refs.APITokenFile)
	if err != nil {
		return Credentials{}, err
	}
	return Credentials{Email: email, APIToken: token}, nil
}

func (resolver CredentialResolver) resolveCredentialsFile(refs config.AtlassianCredentialRefs) (Credentials, error) {
	if strings.TrimSpace(refs.EmailEnv) != "" || strings.TrimSpace(refs.EmailFile) != "" || strings.TrimSpace(refs.APITokenEnv) != "" || strings.TrimSpace(refs.APITokenFile) != "" {
		return Credentials{}, safeCredentialError("atlassian", "credentials file must not be combined with other references")
	}
	content, err := resolver.readFile(strings.TrimSpace(refs.CredentialsFile))
	if err != nil {
		return Credentials{}, safeCredentialError("atlassian", "credentials file reference cannot be read")
	}
	var file struct {
		Email    string `json:"email"`
		APIToken string `json:"api_token"`
	}
	if err := json.Unmarshal(content, &file); err != nil {
		return Credentials{}, safeCredentialError("atlassian", "credentials file cannot be decoded")
	}
	credentials := Credentials{
		Email:    strings.TrimSpace(file.Email),
		APIToken: strings.TrimSpace(file.APIToken),
	}
	if credentials.Email == "" || credentials.APIToken == "" {
		return Credentials{}, safeCredentialError("atlassian", "credentials file is incomplete")
	}
	return credentials, nil
}

func (resolver CredentialResolver) resolveValue(kind, envRef, fileRef string) (string, error) {
	envRef = strings.TrimSpace(envRef)
	fileRef = strings.TrimSpace(fileRef)
	hasEnv := envRef != ""
	hasFile := fileRef != ""
	if hasEnv == hasFile {
		return "", safeCredentialError(kind, "must use exactly one env or file reference")
	}
	if hasEnv {
		value, ok := resolver.lookupEnv(envRef)
		if !ok {
			return "", safeCredentialError(kind, "env reference is unset")
		}
		value = strings.TrimSpace(value)
		if value == "" {
			return "", safeCredentialError(kind, "env reference is empty")
		}
		return value, nil
	}
	content, err := resolver.readFile(fileRef)
	if err != nil {
		return "", safeCredentialError(kind, "file reference cannot be read")
	}
	value := strings.TrimSpace(string(content))
	if value == "" {
		return "", safeCredentialError(kind, "file reference is empty")
	}
	return value, nil
}

func (resolver CredentialResolver) lookupEnv(name string) (string, bool) {
	if resolver.LookupEnv != nil {
		return resolver.LookupEnv(name)
	}
	return os.LookupEnv(name)
}

func (resolver CredentialResolver) readFile(path string) ([]byte, error) {
	if resolver.ReadFile != nil {
		return resolver.ReadFile(path)
	}
	return os.ReadFile(path)
}

func safeCredentialError(kind, detail string) error {
	return fmt.Errorf("%w: %s credential %s", ErrCredentialUnavailable, kind, detail)
}
