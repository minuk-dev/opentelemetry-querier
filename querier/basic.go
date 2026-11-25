package querier

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"strconv"
	"strings"

	"github.com/minuk-dev/opentelemetry-querier/pipeline"
	"github.com/minuk-dev/opentelemetry-querier/qdata"
	"github.com/minuk-dev/opentelemetry-querier/qerror"
	"golang.org/x/crypto/bcrypt"
)

// BasicIdentityConfig authenticates clients against mounted bcrypt hashes.
type BasicIdentityConfig struct {
	Authority string                  `mapstructure:"authority"`
	Realm     string                  `mapstructure:"realm"`
	Accounts  map[string]BasicAccount `mapstructure:"accounts"`
}

// BasicAccount maps one username to a password hash and verified tenant scope.
type BasicAccount struct {
	HashFile string   `mapstructure:"hash_file"`
	Tenants  []string `mapstructure:"tenants"`
}

func basicAuthenticator(cfg BasicIdentityConfig) (pipeline.Authenticator, error) {
	if cfg.Authority == "" || cfg.Realm == "" || len(cfg.Accounts) == 0 {
		return nil, errors.New("basic identity requires authority, realm and accounts")
	}
	if strings.ContainsFunc(cfg.Realm, func(r rune) bool { return r < 32 || r > 126 }) {
		return nil, errors.New("basic realm must contain printable ASCII")
	}

	type account struct {
		hash    []byte
		tenants []string
	}
	var fallback account
	accounts := make(map[string]account, len(cfg.Accounts))
	for name, values := range cfg.Accounts {
		if name == "" || strings.Contains(name, ":") || len(values.Tenants) == 0 {
			return nil, errors.New("each basic account needs a valid username and tenant scope")
		}
		data, err := os.ReadFile(values.HashFile)
		if err != nil {
			return nil, fmt.Errorf("basic hash file: %w", err)
		}
		hash := []byte(strings.TrimSpace(string(data)))
		cost, err := bcrypt.Cost(hash)
		if err != nil || cost < 10 || cost > 16 {
			return nil, errors.New("basic hash must be bcrypt with cost between 10 and 16")
		}
		fallback = account{hash: hash, tenants: values.Tenants}
		accounts[name] = fallback
	}
	challenge := "Basic realm=" + strconv.Quote(cfg.Realm)
	return func(_ context.Context, query *qdata.Query) (pipeline.Principal, error) {
		denied := func() (pipeline.Principal, error) {
			err := qerror.New(qerror.CodeUnauthenticated, "invalid basic credentials")
			err.Challenge = challenge
			return pipeline.Principal{}, err
		}
		values := qdata.HeaderValuesFor(query, "Authorization")
		if len(values) != 1 || len(values[0]) > 8192 {
			return denied()
		}
		request := http.Request{Header: http.Header{"Authorization": values}}
		username, password, ok := request.BasicAuth()
		if !ok || len(password) > 72 {
			return denied()
		}
		record, found := accounts[username]
		if !found {
			record = fallback
		}
		if bcrypt.CompareHashAndPassword(record.hash, []byte(password)) != nil || !found {
			return denied()
		}
		return pipeline.Principal{
			Identity: qdata.Identity{Authority: cfg.Authority, Principal: username},
			Tenants:  record.tenants,
		}, nil
	}, nil
}
