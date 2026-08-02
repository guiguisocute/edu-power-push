package oauth

import "testing"

func service() *Service {
	return NewService(Fallback{
		BaseURL:            "https://env.example.test/",
		GoogleClientID:     "env-google-id",
		GoogleClientSecret: "env-google-secret",
	}, nil, nil)
}

// 按字段兜底：面板只补 GitHub 时 Google 仍用 .env。
func TestMergeFallsBackPerField(t *testing.T) {
	effective := service().Merge(Settings{GitHubClientID: "panel-github-id"},
		map[string]string{SecretGitHubClientSecret: "panel-github-secret"})

	if !effective.Configured(ProviderGoogle) {
		t.Fatalf("google should stay configured from env, missing=%v", effective.Missing(ProviderGoogle))
	}
	if !effective.Configured(ProviderGitHub) {
		t.Fatalf("github should be configured from the panel, missing=%v", effective.Missing(ProviderGitHub))
	}
	if effective.Settings.BaseURL != "https://env.example.test" {
		t.Fatalf("base_url = %q, want the env value with its trailing slash trimmed", effective.Settings.BaseURL)
	}
}

func TestMergePanelOverridesEnv(t *testing.T) {
	effective := service().Merge(
		Settings{BaseURL: "https://panel.example.test", GoogleClientID: "panel-google-id"},
		map[string]string{SecretGoogleClientSecret: "panel-google-secret"},
	)
	if effective.Settings.GoogleClientID != "panel-google-id" || effective.GoogleClientSecret != "panel-google-secret" {
		t.Fatalf("panel values must win: %+v", effective.Settings)
	}
	want := "https://panel.example.test/api/v1/auth/oauth/google/callback"
	if got := effective.RedirectURI(ProviderGoogle); got != want {
		t.Fatalf("redirect uri = %q, want %q", got, want)
	}
}

// 半份凭证必须拒绝，否则按钮静默消失。
func TestValidateEffectiveRejectsHalfCredentials(t *testing.T) {
	empty := NewService(Fallback{}, nil, nil)

	onlyID := empty.Merge(Settings{BaseURL: "https://a.test", GitHubClientID: "id"}, nil)
	if err := ValidateEffective(onlyID); err == nil {
		t.Error("a client id without a secret must be rejected")
	}
	onlySecret := empty.Merge(Settings{BaseURL: "https://a.test"},
		map[string]string{SecretGitHubClientSecret: "secret"})
	if err := ValidateEffective(onlySecret); err == nil {
		t.Error("a secret without a client id must be rejected")
	}
	noBase := empty.Merge(Settings{GitHubClientID: "id"},
		map[string]string{SecretGitHubClientSecret: "secret"})
	if err := ValidateEffective(noBase); err == nil {
		t.Error("credentials without a base url must be rejected: the redirect uri cannot be built")
	}
	complete := empty.Merge(Settings{BaseURL: "https://a.test", GitHubClientID: "id"},
		map[string]string{SecretGitHubClientSecret: "secret"})
	if err := ValidateEffective(complete); err != nil {
		t.Errorf("a complete pair must pass: %v", err)
	}
	// 两家皆未配置为合法可选状态。
	if err := ValidateEffective(empty.Merge(Settings{}, nil)); err != nil {
		t.Errorf("an empty configuration must pass: %v", err)
	}
}

func TestValidateSettingsRejectsBadBaseURL(t *testing.T) {
	for _, bad := range []string{"power.example.edu", "ftp://a.test", "https://a.test/app", "//a.test"} {
		if err := ValidateSettings(NormalizeSettings(Settings{BaseURL: bad})); err == nil {
			t.Errorf("base_url %q must be rejected", bad)
		}
	}
	for _, good := range []string{"", "https://a.test", "http://127.0.0.1:8080", "https://a.test/"} {
		if err := ValidateSettings(NormalizeSettings(Settings{BaseURL: good})); err != nil {
			t.Errorf("base_url %q must be accepted: %v", good, err)
		}
	}
}

// 无 base_url 时 RedirectURI 必须为空。
func TestRedirectURIEmptyWithoutBaseURL(t *testing.T) {
	effective := NewService(Fallback{}, nil, nil).Merge(Settings{}, nil)
	if got := effective.RedirectURI(ProviderGoogle); got != "" {
		t.Fatalf("redirect uri = %q, want empty", got)
	}
}
