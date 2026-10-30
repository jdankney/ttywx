# Records docs/demo.gif for the README with https://github.com/charmbracelet/vhs
#   go build -o ttywx . && vhs docs/demo.tape
# --demo injects fake alerts so every threat mode shows up.

Output docs/demo.gif
Set FontSize 14
Set Width 1400
Set Height 820
Set Theme "Catppuccin Mocha"

Hide
Type "TTYWX_CONTACT=you@example.com ./ttywx --demo --config docs/demo.yaml" Enter
Sleep 6s
Show

Sleep 3s
Type "2" Sleep 3s
Right Sleep 1s Down Sleep 2s
Type "3" Sleep 2s
Type "v" Sleep 3s
Type "v" Sleep 3s
Type "a" Sleep 1s Enter Sleep 4s
Escape Sleep 1s
Type "1" Sleep 3s
Type "q"
