package commands

import (
	"strings"
	"testing"
	"unicode/utf8"
)

func TestParse(t *testing.T) {
	tests := []struct {
		in       string
		name     string
		args     string
		ok       bool
		describe string
	}{
		{"!song daft punk", "song", "daft punk", true, "avec arguments"},
		{"  !LURK  ", "lurk", "", true, "espaces et casse"},
		{"!sr   a   b ", "sr", "a   b", true, "espaces internes conservés"},
		{"hello !song x", "", "", false, "pas au début"},
		{"!", "", "", false, "point d'exclamation seul"},
		{"! song", "", "", false, "espace après !"},
		{"", "", "", false, "vide"},
	}
	for _, tt := range tests {
		name, args, ok := Parse(tt.in)
		if name != tt.name || args != tt.args || ok != tt.ok {
			t.Errorf("%s: Parse(%q) = (%q, %q, %v), attendu (%q, %q, %v)",
				tt.describe, tt.in, name, args, ok, tt.name, tt.args, tt.ok)
		}
	}
}

func TestNormalizeAndValidateName(t *testing.T) {
	if got := NormalizeName("  !Lurk "); got != "lurk" {
		t.Fatalf("NormalizeName = %q", got)
	}
	for _, ok := range []string{"lurk", "discord", "été", "a_b", "x1"} {
		if err := ValidateName(ok); err != nil {
			t.Errorf("ValidateName(%q) = %v, attendu nil", ok, err)
		}
	}
	for _, bad := range []string{"", "a b", "a-b", "!x", "song", "sr", "queue", "help", "skip", "np", "addcmd", strings.Repeat("a", 33)} {
		if err := ValidateName(bad); err == nil {
			t.Errorf("ValidateName(%q) = nil, attendu une erreur", bad)
		}
	}
}

func TestValidateResponseAndCooldown(t *testing.T) {
	if ValidateResponse("   ") == nil {
		t.Error("réponse vide acceptée")
	}
	if ValidateResponse(strings.Repeat("é", MaxResponseRunes+1)) == nil {
		t.Error("réponse trop longue acceptée")
	}
	if err := ValidateResponse(strings.Repeat("é", MaxResponseRunes)); err != nil {
		t.Errorf("réponse à la limite refusée : %v", err)
	}
	if ValidateCooldown(-1) == nil || ValidateCooldown(MaxCooldownSeconds+1) == nil {
		t.Error("délai hors bornes accepté")
	}
	if ValidateCooldown(0) != nil || ValidateCooldown(MaxCooldownSeconds) != nil {
		t.Error("délai valide refusé")
	}
}

func TestRenderDoesNotReinterpretArgs(t *testing.T) {
	got := Render("Salut {user} sur {channel} : {args}", Vars{User: "bob", Channel: "chan", Args: "{user}"})
	want := "Salut bob sur chan : {user}"
	if got != want {
		t.Fatalf("Render = %q, attendu %q", got, want)
	}
}

func TestRenderVariables(t *testing.T) {
	v := Vars{User: "bob", Channel: "chan", Args: "@alice et plus", Count: 7, Intn: func(n int) int { return n - 1 }}
	tests := []struct{ in, want string }{
		{"{touser}", "alice"},
		{"{count}e fois", "7e fois"},
		{"{random}", "100"},
		{"{pick: a | b | c }", "c"},
		{"{inconnue}", "{inconnue}"},
	}
	for _, tt := range tests {
		if got := Render(tt.in, v); got != tt.want {
			t.Errorf("Render(%q) = %q, attendu %q", tt.in, got, tt.want)
		}
	}
	if got := Render("{touser}", Vars{User: "bob"}); got != "bob" {
		t.Errorf("{touser} sans argument = %q", got)
	}
}

func TestRenderNeutralisesRelayedCommands(t *testing.T) {
	got := Render("{args}", Vars{Args: "!nightbot-cmd /ban .mod x"})
	if got != "nightbot-cmd ban mod x" {
		t.Fatalf("Render = %q", got)
	}
}

func TestNormalizeAliases(t *testing.T) {
	got, err := NormalizeAliases("discord", []string{" !Dc ", "dc", "discord", "", "serveur"})
	if err != nil || len(got) != 2 || got[0] != "dc" || got[1] != "serveur" {
		t.Fatalf("NormalizeAliases = %v, %v", got, err)
	}
	if _, err := NormalizeAliases("x", []string{"song"}); err == nil {
		t.Error("alias réservé accepté")
	}
	if _, err := NormalizeAliases("x", []string{"a b"}); err == nil {
		t.Error("alias invalide accepté")
	}
}

func TestSanitizeOutgoing(t *testing.T) {
	tests := []struct{ in, want string }{
		{"/ban someone", "ban someone"},
		{".timeout someone 600", "timeout someone 600"},
		{"  /  /mod x", "mod x"},
		{"ligne1\r\nPRIVMSG #x :pwned", "ligne1  PRIVMSG #x :pwned"},
		{"a\x01ACTION b", "a ACTION b"},
		{"normal !", "normal !"},
	}
	for _, tt := range tests {
		if got := SanitizeOutgoing(tt.in); got != tt.want {
			t.Errorf("SanitizeOutgoing(%q) = %q, attendu %q", tt.in, got, tt.want)
		}
	}
	long := SanitizeOutgoing(strings.Repeat("é", 600))
	if n := utf8.RuneCountInString(long); n != MaxMessageRunes {
		t.Errorf("longueur après troncature = %d, attendu %d", n, MaxMessageRunes)
	}
	if !utf8.ValidString(long) {
		t.Error("troncature a produit de l'UTF-8 invalide")
	}
}
