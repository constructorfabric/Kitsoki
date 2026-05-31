# Emit a compact action-trace line per relevant event.
select(.type=="user" or .type=="assistant")
| if .type=="user" then
    (.message.content) as $c
    | if ($c|type)=="string" then
        if ($c|test("^<")) then empty else "USER: " + ($c|gsub("\\s+";" ")|.[0:600]) end
      else
        ($c[]? | select(.type=="text") | .text) // empty
        | if (test("^<command")) or (test("system-reminder")) then empty
          else "USER: " + (gsub("\\s+";" ")|.[0:600]) end
      end
  else
    .message.content[]? as $b
    | if $b.type=="tool_use" then
        ($b.input.command // $b.input.description // $b.input.file_path // $b.input.prompt // $b.input.query // ($b.input|tojson)) as $arg
        | "  > " + $b.name + ": " + (($arg|tostring)|gsub("\\s+";" ")|.[0:200])
      elif $b.type=="text" then
        ($b.text|gsub("\\s+";" ")) as $t
        | if ($t|length) > 8 then "AI: " + ($t|.[0:180]) else empty end
      else empty end
  end
