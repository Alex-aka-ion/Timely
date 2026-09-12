package bot

import "testing"

func TestEventTokens_RoundTrip(t *testing.T) {
	tk := newEventTokens()

	longID := "some-really-long-event-id-imported-from-outlook-or-whatever-service"
	token := tk.tokenFor(longID)

	if len(token) == 0 || len(token) > 20 {
		t.Fatalf("ожидали короткий токен, получили %q (%d байт)", token, len(token))
	}

	got, ok := tk.resolve(token)
	if !ok {
		t.Fatalf("resolve(%q) не нашёл токен", token)
	}
	if got != longID {
		t.Fatalf("resolve вернул %q, ожидали %q", got, longID)
	}

	// Повторный tokenFor с тем же ID должен вернуть тот же токен, а не плодить новые.
	again := tk.tokenFor(longID)
	if again != token {
		t.Fatalf("tokenFor не идемпотентен: %q != %q", again, token)
	}

	if _, ok := tk.resolve("неизвестный-токен"); ok {
		t.Fatal("resolve неожиданно нашёл несуществующий токен")
	}
}
