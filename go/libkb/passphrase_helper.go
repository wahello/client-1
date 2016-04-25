package libkb

import (
	"errors"
	"fmt"
	"strings"

	keybase1 "github.com/keybase/client/go/protocol"
)

func GetKeybasePassphrase(g *GlobalContext, ui SecretUI, username, retryMsg string, allowSecretStore bool) (keybase1.GetPassphraseRes, error) {
	arg := DefaultPassphraseArg(allowSecretStore)
	arg.WindowTitle = "Keybase passphrase"
	arg.Type = keybase1.PassphraseType_PASS_PHRASE
	arg.Username = username
	arg.Prompt = fmt.Sprintf("Please enter the Keybase passphrase for %s (12+ characters)", username)
	arg.RetryLabel = retryMsg
	return GetPassphraseUntilCheckWithChecker(g, arg, newUIPrompter(ui), &CheckPassphraseSimple)
}

func GetSecret(g *GlobalContext, ui SecretUI, title, prompt, retryMsg string, allowSecretStore bool) (keybase1.GetPassphraseRes, error) {
	arg := DefaultPassphraseArg(allowSecretStore)
	arg.WindowTitle = title
	arg.Type = keybase1.PassphraseType_PASS_PHRASE
	arg.Prompt = prompt
	arg.RetryLabel = retryMsg
	// apparently allowSecretStore can be true even though HasSecretStore()
	// is false (in the case of mocked secret store tests on linux, for
	// example). So, pass this through:
	arg.Features.StoreSecret.Allow = allowSecretStore
	return GetPassphraseUntilCheckWithChecker(g, arg, newUIPrompter(ui), &CheckPassphraseSimple)
}

func GetPaperKeyPassphrase(g *GlobalContext, ui SecretUI, username string) (string, error) {
	arg := DefaultPassphraseArg(false)
	arg.WindowTitle = "Paper backup key passphrase"
	arg.Type = keybase1.PassphraseType_PAPER_KEY
	if len(username) == 0 {
		username = "your account"
	}
	arg.Prompt = fmt.Sprintf("Please enter a paper backup key passphrase for %s", username)
	arg.Username = username
	arg.Features.StoreSecret.Allow = false
	arg.Features.StoreSecret.Readonly = true
	arg.Features.ShowTyping.Allow = true
	arg.Features.ShowTyping.DefaultValue = true
	res, err := GetPassphraseUntilCheck(g, arg, newUIPrompter(ui), &PaperChecker{})
	if err != nil {
		return "", err
	}
	return res.Passphrase, nil
}

func GetPaperKeyForCryptoPassphrase(g *GlobalContext, ui SecretUI, reason string, devices []*Device) (string, error) {
	if len(devices) == 0 {
		return "", errors.New("empty device list")
	}
	arg := DefaultPassphraseArg(false)
	arg.WindowTitle = "Paper backup key passphrase"
	arg.Type = keybase1.PassphraseType_PAPER_KEY
	arg.Features.StoreSecret.Allow = false
	arg.Features.StoreSecret.Readonly = true
	if len(devices) == 1 {
		arg.Prompt = fmt.Sprintf("%s: please enter the paper key '%s...'", reason, *devices[0].Description)
	} else {
		descs := make([]string, len(devices))
		for i, dev := range devices {
			descs[i] = fmt.Sprintf("'%s...'", *dev.Description)
		}
		paperOpts := strings.Join(descs, " or ")
		arg.Prompt = fmt.Sprintf("%s: please enter one of the following paper keys %s", reason, paperOpts)
	}

	res, err := GetPassphraseUntilCheck(g, arg, newUIPrompter(ui), &PaperChecker{})
	if err != nil {
		return "", err
	}
	return res.Passphrase, nil
}

type PassphrasePrompter interface {
	Prompt(keybase1.GUIEntryArg) (keybase1.GetPassphraseRes, error)
}

type uiPrompter struct {
	ui SecretUI
}

var _ PassphrasePrompter = &uiPrompter{}

func newUIPrompter(ui SecretUI) *uiPrompter {
	return &uiPrompter{ui: ui}
}

func (u *uiPrompter) Prompt(arg keybase1.GUIEntryArg) (keybase1.GetPassphraseRes, error) {
	return u.ui.GetPassphrase(arg, nil)
}

func GetPassphraseUntilCheckWithChecker(g *GlobalContext, arg keybase1.GUIEntryArg, prompter PassphrasePrompter, checker *Checker) (keybase1.GetPassphraseRes, error) {
	if checker == nil {
		return keybase1.GetPassphraseRes{}, errors.New("nil passphrase checker")
	}
	w := &CheckerWrapper{checker: *checker}
	return GetPassphraseUntilCheck(g, arg, prompter, w)
}

func GetPassphraseUntilCheck(g *GlobalContext, arg keybase1.GUIEntryArg, prompter PassphrasePrompter, checker PassphraseChecker) (keybase1.GetPassphraseRes, error) {
	for i := 0; i < 10; i++ {
		res, err := prompter.Prompt(arg)
		if err != nil {
			return keybase1.GetPassphraseRes{}, err
		}
		if checker == nil {
			return res, nil
		}
		err = checker.Check(g, res.Passphrase)
		if err == nil {
			return res, nil
		}
		arg.RetryLabel = err.Error()
	}

	return keybase1.GetPassphraseRes{}, RetryExhaustedError{}
}

func DefaultPassphraseArg(allowSecretStore bool) keybase1.GUIEntryArg {
	return keybase1.GUIEntryArg{
		SubmitLabel: "Submit",
		CancelLabel: "Cancel",
		Features: keybase1.GUIEntryFeatures{
			ShowTyping: keybase1.Feature{
				Allow:        true,
				DefaultValue: false,
				Readonly:     true,
				Label:        "Show typing",
			},
			StoreSecret: keybase1.Feature{
				Allow:        allowSecretStore,
				DefaultValue: false,
				Readonly:     false,
				Label:        "Save in Keychain",
			},
		},
	}
}

// PassphraseChecker is an interface for checking the format of a
// passphrase. Returns nil if the format is ok, or a descriptive
// hint otherwise.
type PassphraseChecker interface {
	Check(*GlobalContext, string) error
}

// CheckerWrapper wraps a Checker type to make it conform to the
// PassphraseChecker interface.
type CheckerWrapper struct {
	checker Checker
}

// Check s using checker, respond with checker.Hint if check
// fails.
func (w *CheckerWrapper) Check(_ *GlobalContext, s string) error {
	if w.checker.F(s) {
		return nil
	}
	return errors.New(w.checker.Hint)
}

// PaperChecker implements PassphraseChecker for paper keys.
type PaperChecker struct{}

// Check a paper key format.  Will return a detailed error message
// specific to the problems found in s.
func (p *PaperChecker) Check(g *GlobalContext, s string) error {
	phrase := NewPaperKeyPhrase(s)

	// check for empty
	if len(phrase.String()) == 0 {
		g.Log.Debug("paper phrase is empty")
		return errors.New("Empty paper key. Please try again.")
	}

	// check for invalid words
	invalids := phrase.InvalidWords()
	if len(invalids) > 0 {
		g.Log.Debug("paper phrase has invalid word(s) in it")
		if len(invalids) > 1 {
			return fmt.Errorf("Please try again. These words are invalid: %s", strings.Join(invalids, ", "))
		}
		return fmt.Errorf("Please try again. This word is invalid: %s", invalids[0])
	}

	// check version
	version, err := phrase.Version()
	if err != nil {
		g.Log.Debug("error getting paper key version: %s", err)
		// despite the error, just tell the user there was a typo:
		return errors.New("It looks like there was a typo in the paper key. Please try again.")
	}
	if version != PaperKeyVersion {
		g.Log.Debug("paper key version mismatch: generated version = %d, libkb version = %d", version, PaperKeyVersion)
		return fmt.Errorf("It looks like there was a typo. The paper key you entered had an invalid version. Please try again.")
	}

	return nil
}
