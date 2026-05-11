# VHS tape for the Cloak of Darkness demo.
# Run: vhs demo/cloak.tape
# Output: demo/cloak.gif
# Requires: vhs (charmbracelet/vhs) to be installed.
# If vhs is not installed, install with: go install github.com/charmbracelet/vhs@latest
# (requires ffmpeg and chromium/ttyd)

Output demo/cloak.gif

Set Width 1200
Set Height 800
Set FontSize 14
Set FontFamily "JetBrains Mono"
Set Theme "Molokai"

# Launch kitsoki with the replay harness against the Cloak of Darkness recording.
Type "kitsoki run testdata/apps/cloak/app.yaml --harness replay --recording testdata/apps/cloak/recording.yaml"
Enter
Sleep 2s

# Turn 1: go west to the cloakroom.
Type@200ms "go west"
Enter
Sleep 1s

# Turn 2: hang the cloak.
Type@200ms "hang the cloak"
Enter
Sleep 1s

# Turn 3: head east back to foyer.
Type@200ms "head east"
Enter
Sleep 1s

# Turn 4: go south to the bar (now lit).
Type@200ms "go south"
Enter
Sleep 1s

# Turn 5: read the message — winning ending.
Type@200ms "read the message"
Enter
Sleep 3s
