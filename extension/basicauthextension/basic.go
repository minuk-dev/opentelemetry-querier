// Package basicauthextension authenticates credentials against mounted bcrypt hashes.
package basicauthextension

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"strconv"
	"strings"

	"github.com/minuk-dev/opentelemetry-querier/acceptor"
	"github.com/minuk-dev/opentelemetry-querier/component"
	"github.com/minuk-dev/opentelemetry-querier/component/extension"
	"github.com/minuk-dev/opentelemetry-querier/qdata"
	"github.com/minuk-dev/opentelemetry-querier/qerror"
	"golang.org/x/crypto/bcrypt"
)

// Config authenticates clients against mounted bcrypt hashes.
type Config struct {
	Authority string                  `mapstructure:"authority"`
	Realm     string                  `mapstructure:"realm"`
	Accounts  map[string]BasicAccount `mapstructure:"accounts"`
}

// BasicAccount maps one username to a password hash and verified tenant scope.
type BasicAccount struct {
	HashFile string   `mapstructure:"hash_file"`
	Tenants  []string `mapstructure:"tenants"`
}

var (
	errBasicConfig  = errors.New("basic identity requires authority, realm and accounts")
	errBasicRealm   = errors.New("basic realm must contain printable ASCII")
	errBasicAccount = errors.New("each basic account needs a valid username and tenant scope")
	errBasicHash    = errors.New("basic hash must be bcrypt with cost between 10 and 16")
)

const (
	maxBasicHeaderBytes    = 8192
	maxBcryptPasswordBytes = 72
	minBcryptCost          = 10
	maxBcryptCost          = 16
)

type basicAccountRecord struct {
	hash    []byte
	tenants []string
}

type basicCredentials struct {
	authority string
	challenge string
	accounts  map[string]basicAccountRecord
	fallback  basicAccountRecord
}

func basicAuthenticator(cfg Config) (acceptor.AuthenticatorFunc, error) {
	if cfg.Authority == "" || cfg.Realm == "" || len(cfg.Accounts) == 0 {
		return nil, errBasicConfig
	}

	if strings.ContainsFunc(cfg.Realm, func(character rune) bool { return character < ' ' || character > '~' }) {
		return nil, errBasicRealm
	}

	store := basicCredentials{
		authority: cfg.Authority,
		challenge: "Basic realm=" + strconv.Quote(cfg.Realm),
		accounts:  make(map[string]basicAccountRecord, len(cfg.Accounts)),
		fallback:  basicAccountRecord{hash: nil, tenants: nil},
	}
	for name, values := range cfg.Accounts {
		record, err := loadBasicAccount(name, values)
		if err != nil {
			return nil, err
		}

		store.accounts[name] = record
		store.fallback = record
	}

	return store.authenticate, nil
}

func loadBasicAccount(name string, values BasicAccount) (basicAccountRecord, error) {
	var empty basicAccountRecord
	if name == "" || strings.Contains(name, ":") || len(values.Tenants) == 0 {
		return empty, errBasicAccount
	}

	data, err := os.ReadFile(values.HashFile)
	if err != nil {
		return empty, fmt.Errorf("basic hash file: %w", err)
	}

	hash := []byte(strings.TrimSpace(string(data)))

	cost, err := bcrypt.Cost(hash)
	if err != nil || cost < minBcryptCost || cost > maxBcryptCost {
		return empty, errBasicHash
	}

	return basicAccountRecord{hash: hash, tenants: values.Tenants}, nil
}

func (store basicCredentials) authenticate(_ context.Context, headers http.Header) (acceptor.Principal, error) {
	denied := func() (acceptor.Principal, error) {
		err := qerror.New(qerror.CodeUnauthenticated, "invalid basic credentials")
		err.Challenge = store.challenge

		return acceptor.Principal{}, err
	}

	values := headers.Values("Authorization")
	if len(values) != 1 || len(values[0]) > maxBasicHeaderBytes {
		return denied()
	}

	var request http.Request

	request.Header = http.Header{"Authorization": values}

	username, password, ok := request.BasicAuth()
	if !ok || len(password) > maxBcryptPasswordBytes {
		return denied()
	}

	record, found := store.accounts[username]
	if !found {
		record = store.fallback
	}

	if bcrypt.CompareHashAndPassword(record.hash, []byte(password)) != nil || !found {
		return denied()
	}

	return acceptor.Principal{
		Identity: qdata.Identity{
			Authority: store.authority, Principal: username, Tenant: "",
			Groups: nil, Roles: nil, Scopes: nil,
		},
		Tenants: record.tenants,
	}, nil
}

var errInvalidConfig = errors.New("basicauth: invalid config type")

// NewFactory registers the basicauth extension.
func NewFactory() extension.Factory {
	return extension.NewFactory(
		component.MustNewType("basicauth"), func() component.Config { return new(Config) }, create,
	)
}

func create(_ context.Context, _ component.Settings, cfg component.Config) (extension.Extension, error) {
	config, ok := cfg.(*Config)
	if !ok {
		return nil, errInvalidConfig
	}

	return basicAuthenticator(*config)
}
