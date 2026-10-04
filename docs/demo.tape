# The README animation. Regenerate with `moon run demo`.

Output docs/demo.gif

Set Shell zsh
Set FontSize 18
Set Width 1200
Set Height 720
Set Padding 24
Set Theme "GitHub Dark"
Set WindowBar Colorful
Set TypingSpeed 80ms

Hide
# Keep the recording in color even when the caller disables terminal colors.
Env NO_COLOR ""
Env TERM "xterm-256color"
Env COLORTERM "truecolor"
Type "source docs/demo-sandbox.sh"
Enter
Sleep 15s
Show

Sleep 800ms
Type "lopper ~/code"
Sleep 600ms
Enter
Sleep 3.2s

# Sort by size and use search so selection does not depend on scan order.
Type "s"
Sleep 800ms
Type "/merged"
Enter
Up@30ms 11
Sleep 800ms
Space@450ms 4
Escape
Sleep 600ms

Type "/uncommitted"
Enter
Up@30ms 11
Sleep 800ms
Space@450ms 2
Escape
Sleep 1.5s

Type "d"
Sleep 3.5s
Enter

# Removal takes as long as the disk does; hold once the result is up. The
# loop starts over from here.
Wait+Screen@20s /freed/
Sleep 5.5s
