package httpapi

import "testing"

// ?next= 决定登录成功后的返回页。原样重定向会造成开放重定向。
func TestSafeReturnPathRejectsOffSiteTargets(t *testing.T) {
	cases := map[string]string{
		"":                        "/",
		"/":                       "/",
		"/usage":                  "/usage",
		"/admin?tab=users":        "/admin?tab=users",
		"//evil.example":          "/",
		"/\\evil.example":         "/",
		"https://evil.example":    "/",
		"http://evil.example/x":   "/",
		"evil.example":            "/",
		"javascript:alert(1)":     "/",
		"/redirect?to=://evil.io": "/",
	}
	for input, want := range cases {
		if got := safeReturnPath(input); got != want {
			t.Errorf("safeReturnPath(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestSecureEqual(t *testing.T) {
	if !secureEqual("abc", "abc") {
		t.Error("identical states must compare equal")
	}
	if secureEqual("abc", "abd") || secureEqual("abc", "ab") || secureEqual("", "x") {
		t.Error("different states must not compare equal")
	}
}
