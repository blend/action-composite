// Copyright 2021 Blend Labs, Inc.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     https://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package composite_test

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/blend/go-sdk/assert"
	"github.com/google/go-github/v40/github"
	githubactions "github.com/sethvargo/go-githubactions"

	"github.com/blend/action-composite/pkg/composite"
	githubshim "github.com/blend/action-composite/pkg/github"
)

func TestNewFromInputs(t *testing.T) {
	t.Parallel()
	it := assert.New(t)

	// Error: invalid `timeout`
	action := githubactions.New(githubactions.WithGetenv(
		getenvFromMap(map[string]string{
			"INPUT_TIMEOUT": "y",
		}),
	))
	cfg, err := composite.NewFromInputs(action)
	it.Nil(cfg)
	it.Equal("Invalid input; Input: \"timeout\", Value: \"y\"\ntime: invalid duration \"y\"", fmt.Sprintf("%v", err))

	// Error: invalid `interval`
	action = githubactions.New(githubactions.WithGetenv(
		getenvFromMap(map[string]string{
			"INPUT_TIMEOUT":  "30m",
			"INPUT_INTERVAL": "z",
		}),
	))
	cfg, err = composite.NewFromInputs(action)
	it.Nil(cfg)
	it.Equal("Invalid input; Input: \"interval\", Value: \"z\"\ntime: invalid duration \"z\"", fmt.Sprintf("%v", err))

	// Invalid `GITHUB_EVENT_PATH`
	eventPath := writeTemp(it, []byte("{"))
	action = githubactions.New(githubactions.WithGetenv(
		getenvFromMap(map[string]string{
			"INPUT_GITHUB-TOKEN": "561427eed114801b0f69b28593c0ce4ab193d038",
			"INPUT_TIMEOUT":      "30m",
			"INPUT_INTERVAL":     "30s",
			"INPUT_CHECKS-YAML":  "- paths: []\n",
			"GITHUB_EVENT_PATH":  eventPath,
			"GITHUB_REPOSITORY":  "mess/clean",
			"GITHUB_EVENT_NAME":  "pull_request",
		}),
	))
	cfg, err = composite.NewFromInputs(action)
	it.Nil(cfg)
	expectedErr := fmt.Sprintf("Failed to parse GitHub Event file as JSON; Path: %s\nunexpected end of JSON input", eventPath)
	it.Equal(expectedErr, fmt.Sprintf("%v", err))

	// Invalid `GITHUB_REPOSITORY`
	eventPath, err = filepath.Abs(filepath.Join("testdata", "event.json"))
	it.Nil(err)
	action = githubactions.New(githubactions.WithGetenv(
		getenvFromMap(map[string]string{
			"INPUT_GITHUB-TOKEN": "561427eed114801b0f69b28593c0ce4ab193d038",
			"INPUT_TIMEOUT":      "30m",
			"INPUT_INTERVAL":     "30s",
			"INPUT_CHECKS-YAML":  "- paths: []\n",
			"GITHUB_EVENT_PATH":  eventPath,
			"GITHUB_REPOSITORY":  "",
			"GITHUB_EVENT_NAME":  "pull_request",
		}),
	))
	cfg, err = composite.NewFromInputs(action)
	it.Nil(cfg)
	it.Equal(`Unexpected GitHub repository format; Repository: ""`, fmt.Sprintf("%v", err))

	// Happy Path
	eventPath, err = filepath.Abs(filepath.Join("testdata", "event.json"))
	it.Nil(err)
	action = githubactions.New(githubactions.WithGetenv(
		getenvFromMap(map[string]string{
			"INPUT_GITHUB-TOKEN": "561427eed114801b0f69b28593c0ce4ab193d038",
			"INPUT_TIMEOUT":      "31m",
			"INPUT_INTERVAL":     "37s",
			"INPUT_CHECKS-YAML":  "- job: court\n  paths:\n  - spotlight/**\n  - docs/**\n",
			"GITHUB_EVENT_PATH":  eventPath,
			"GITHUB_REPOSITORY":  "mess/clean",
			"GITHUB_EVENT_NAME":  "pull_request",
			"GITHUB_API_URL":     "https://ghe.k8s.invalid/api/v3",
		}),
	))
	cfg, err = composite.NewFromInputs(action)
	it.Nil(err)
	expected := &composite.Config{
		GitHubToken:   "561427eed114801b0f69b28593c0ce4ab193d038",
		Timeout:       31 * time.Minute,
		Interval:      37 * time.Second,
		ChecksYAML:    "- job: court\n  paths:\n  - spotlight/**\n  - docs/**",
		GitHubRootURL: "https://ghe.k8s.invalid/api/v3",
		EventName:     "pull_request",
		EventAction:   "opened",
		GitHubOrg:     "mess",
		GitHubRepo:    "clean",
		BaseSHA:       "ef3237727fcb36295e462cd2c2b71e38d48fd772",
		HeadSHA:       "fb8bcd85860b706ad2d5a776775b4ad9bbf2520f",
	}
	it.Equal(expected, cfg)
}

func TestConfig_Validate(t *testing.T) {
	t.Parallel()
	it := assert.New(t)

	// Failure; `EventName`
	c := composite.Config{EventName: "push"}
	err := c.Validate()
	it.Equal(`The Composite Action can only run on pull requests; Event Name: "push"`, fmt.Sprintf("%v", err))

	// Failure; `EventAction`
	c = composite.Config{
		EventName:   "pull_request",
		EventAction: "converted_to_draft",
	}
	err = c.Validate()
	it.Equal(`The Composite Action can only run on pull request types spawned by code changes; Event Action: "converted_to_draft"`, fmt.Sprintf("%v", err))

	// Failure; `BaseSHA`
	c = composite.Config{
		EventName:   "pull_request",
		EventAction: "synchronize",
	}
	err = c.Validate()
	it.Equal("Could not determine the base SHA for this pull request", fmt.Sprintf("%v", err))

	// Failure; `HeadSHA`
	c = composite.Config{
		EventName:   "pull_request",
		EventAction: "reopened",
		BaseSHA:     "5063feca9073b0c72c9e5b8b8528702ee16a59e5",
	}
	err = c.Validate()
	it.Equal("Could not determine the head SHA for this pull request", fmt.Sprintf("%v", err))

	// Failure; `GitHubOrg`
	c = composite.Config{
		EventName:   "pull_request",
		EventAction: "opened",
		BaseSHA:     "5063feca9073b0c72c9e5b8b8528702ee16a59e5",
		HeadSHA:     "5d87b421641a22dac8981bfe98be7e9d1cece8e0",
	}
	err = c.Validate()
	it.Equal("The Composite Action requires a GitHub owner or org", fmt.Sprintf("%v", err))

	// Failure; `GitHubRepo`
	c = composite.Config{
		EventName:   "pull_request",
		EventAction: "opened",
		GitHubOrg:   "look",
		BaseSHA:     "5063feca9073b0c72c9e5b8b8528702ee16a59e5",
		HeadSHA:     "5d87b421641a22dac8981bfe98be7e9d1cece8e0",
	}
	err = c.Validate()
	it.Equal("The Composite Action requires a GitHub repository", fmt.Sprintf("%v", err))

	// Failure; `GitHubRootURL`
	c = composite.Config{
		EventName:   "pull_request",
		EventAction: "opened",
		GitHubOrg:   "look",
		GitHubRepo:  "day",
		BaseSHA:     "5063feca9073b0c72c9e5b8b8528702ee16a59e5",
		HeadSHA:     "5d87b421641a22dac8981bfe98be7e9d1cece8e0",
	}
	err = c.Validate()
	it.Equal("The Composite Action requires a GitHub root URL", fmt.Sprintf("%v", err))

	// Failure; `GitHubToken`
	c = composite.Config{
		GitHubRootURL: "https://ghe.k8s.invalid/api/v3",
		EventName:     "pull_request",
		EventAction:   "opened",
		GitHubOrg:     "look",
		GitHubRepo:    "day",
		BaseSHA:       "5063feca9073b0c72c9e5b8b8528702ee16a59e5",
		HeadSHA:       "5d87b421641a22dac8981bfe98be7e9d1cece8e0",
	}
	err = c.Validate()
	it.Equal("The Composite Action requires a GitHub API token", fmt.Sprintf("%v", err))

	// Failure; neither `ChecksYAML` and `ChecksFilename`
	c = composite.Config{
		GitHubToken:   "03d3afa0ee2b533f112c8021e7f7edd9ff00da22",
		GitHubRootURL: "https://ghe.k8s.invalid/api/v3",
		EventName:     "pull_request",
		EventAction:   "opened",
		GitHubOrg:     "look",
		GitHubRepo:    "day",
		BaseSHA:       "5063feca9073b0c72c9e5b8b8528702ee16a59e5",
		HeadSHA:       "5d87b421641a22dac8981bfe98be7e9d1cece8e0",
	}
	err = c.Validate()
	it.Equal("The Composite Action requires exactly one of checks YAML or checks filename; neither are set", fmt.Sprintf("%v", err))

	// Failure; both `ChecksYAML` and `ChecksFilename`
	c = composite.Config{
		GitHubToken:    "03d3afa0ee2b533f112c8021e7f7edd9ff00da22",
		ChecksYAML:     "- job: court\n  paths:\n  - spotlight/**\n  - docs/**",
		ChecksFilename: ".github/monorepo/hoops.yml",
		GitHubRootURL:  "https://ghe.k8s.invalid/api/v3",
		EventName:      "pull_request",
		EventAction:    "opened",
		GitHubOrg:      "look",
		GitHubRepo:     "day",
		BaseSHA:        "5063feca9073b0c72c9e5b8b8528702ee16a59e5",
		HeadSHA:        "5d87b421641a22dac8981bfe98be7e9d1cece8e0",
	}
	err = c.Validate()
	it.Equal("The Composite Action requires exactly one of checks YAML or checks filename; both are set", fmt.Sprintf("%v", err))
}

func TestConfig_GetChecks(t *testing.T) {
	t.Parallel()
	it := assert.New(t)

	ctx := context.TODO()

	// Error: invalid `checks-yaml`
	c := composite.Config{ChecksYAML: "- paths: 'abc''"}
	checks, err := c.GetChecks(ctx, &github.Client{})
	it.Nil(checks)
	it.Equal("Failed to parse checks file as YAML\nyaml: found unexpected end of stream", fmt.Sprintf("%v", err))

	// Happy path: valid `checks-yaml`
	c = composite.Config{ChecksYAML: "- job: court\n  paths:\n  - spotlight/**\n  - docs/**\n"}
	checks, err = c.GetChecks(ctx, &github.Client{})
	it.Nil(err)
	expected := []composite.Check{
		{
			Job:   "court",
			Paths: []string{"spotlight/**", "docs/**"},
		},
	}
	it.Equal(expected, checks)

	// Error: fails GitHub API call
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		w.Write([]byte("404: Not Found"))
	}))
	t.Cleanup(server.Close)
	client, err := githubshim.NewClient(ctx, server.URL+"/api/v3", "test-token")
	it.Nil(err)

	c = composite.Config{
		ChecksFilename: ".github/not-exist.yml",
		GitHubOrg:      "fish",
		GitHubRepo:     "bowl",
		HeadSHA:        "c37f875d7a90cabf793847a1a20d980b56febc16",
	}
	checks, err = c.GetChecks(ctx, client)
	it.Nil(checks)
	expectedErr := fmt.Sprintf(
		"Failed to download file; Repository: fish/bowl, Ref: c37f875d7a90cabf793847a1a20d980b56febc16, Path: .github/not-exist.yml\nGET %s/api/v3/repos/fish/bowl/contents/.github?ref=c37f875d7a90cabf793847a1a20d980b56febc16: 404  []",
		server.URL,
	)
	it.Equal(expectedErr, fmt.Sprintf("%v", err))
}

func getenvFromMap(m map[string]string) githubactions.GetenvFunc {
	return func(key string) string {
		return m[key]
	}
}

func writeTemp(it *assert.Assertions, data []byte) string {
	f, err := os.CreateTemp("", "")
	it.Nil(err)
	_, err = f.Write(data)
	it.Nil(err)
	err = f.Close()
	it.Nil(err)

	it.T.Cleanup(func() {
		err := os.Remove(f.Name())
		it.Nil(err)
	})

	return f.Name()
}
