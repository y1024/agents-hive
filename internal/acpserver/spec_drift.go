package acpserver

import (
	"errors"
	"fmt"
	"sort"
	"strings"
)

var ErrACPMethodFixtureDrift = errors.New("acp method fixture drift")

type ACPMethodFixture struct {
	Name    string
	Methods []string
}

type ACPMethodDriftResult struct {
	Fixture   string
	Missing   []string
	Extra     []string
	Invalid   []string
	Duplicate []string
}

func CheckACPMethodDrift(fixture ACPMethodFixture) ACPMethodDriftResult {
	expected := setFromStrings(SupportedACPMethods())
	seen := make(map[string]bool, len(fixture.Methods))
	actual := make(map[string]bool, len(fixture.Methods))
	result := ACPMethodDriftResult{Fixture: fixture.Name}

	for _, method := range fixture.Methods {
		if strings.TrimSpace(method) == "" {
			result.Invalid = append(result.Invalid, method)
			continue
		}
		if seen[method] {
			result.Duplicate = append(result.Duplicate, method)
			continue
		}
		seen[method] = true
		actual[method] = true
		if !expected[method] {
			result.Extra = append(result.Extra, method)
		}
	}

	for _, method := range SupportedACPMethods() {
		if !actual[method] {
			result.Missing = append(result.Missing, method)
		}
	}
	sort.Strings(result.Extra)
	sort.Strings(result.Invalid)
	sort.Strings(result.Duplicate)
	return result
}

func (r ACPMethodDriftResult) HasDrift() bool {
	return len(r.Missing) > 0 || len(r.Extra) > 0 || len(r.Invalid) > 0 || len(r.Duplicate) > 0
}

func (r ACPMethodDriftResult) Err() error {
	if !r.HasDrift() {
		return nil
	}
	return fmt.Errorf("%w: fixture=%s missing=%v extra=%v invalid=%v duplicate=%v",
		ErrACPMethodFixtureDrift,
		r.Fixture,
		r.Missing,
		r.Extra,
		r.Invalid,
		r.Duplicate,
	)
}

func setFromStrings(values []string) map[string]bool {
	out := make(map[string]bool, len(values))
	for _, value := range values {
		out[value] = true
	}
	return out
}
