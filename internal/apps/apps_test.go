package apps

import (
	"strings"
	"testing"
)

const sample = `
--------------------------------------------------------------------------------
bundle id:                  Simulator (0xc20)
path:                       /Applications/Xcode.app/Contents/Developer/Applications/Simulator.app (0x1c2c)
name:                       Simulator
identifier:                 com.apple.iphonesimulator
--------------------------------------------------------------------------------
bundle id:                  com.example.buildreport.service (0x183c)
path:                       /Users/me/Work/x/target/site/report/com.example.buildreport.service (0x2880)
--------------------------------------------------------------------------------
bundle id:                  Chrome (0x1854)
path:                       /Applications/Google Chrome.app (0x2ae0)
directory:                  /Applications
name:                       Chrome
teamID:                     EQHXZ8M8AV
identifier:                 com.google.Chrome
--------------------------------------------------------------------------------
path:                       /Users/me/Applications/PyCharm.app (0x1)
identifier:                 com.jetbrains.pycharm
--------------------------------------------------------------------------------
path:                       /Volumes/Ext/Foo.app (0x2)
identifier:                 com.example.foo
`

func TestParseLSRegister(t *testing.T) {
	idx := parseLSRegister(strings.NewReader(sample), Roots("/Users/me"))
	ok, _ := idx.InstalledByName("Google Chrome")
	if !ok {
		t.Error("Chrome should be installed")
	}
	if ok, _ := idx.InstalledByName("pycharm"); !ok {
		t.Error("PyCharm (case-insensitive) should be installed")
	}
	if ok, _ := idx.InstalledByName("Simulator"); !ok {
		t.Error("nested bundle under /Applications should count")
	}
	if ok, _ := idx.BundleRegistered("com.google.chrome"); !ok {
		t.Error("bundle id lookup failed")
	}
	found := false
	for _, a := range idx.All() {
		if a.Name == "Google Chrome" && a.TeamID == "EQHXZ8M8AV" && a.BundleID == "com.google.chrome" {
			found = true
		}
	}
	if !found {
		t.Errorf("All() lacks team id: %+v", idx.All())
	}
	if ok, _ := idx.InstalledByName("Foo"); ok {
		t.Error("/Volumes app must not count")
	}
	if ok, _ := idx.BundleRegistered("com.example.buildreport.service"); ok {
		t.Error("non-.app record must not count")
	}
	if _, err := Failed(errIndex).InstalledByName("x"); err == nil {
		t.Error("failed index must return error")
	}
}

var errIndex = strings.NewReader("").UnreadByte()
