# Demonstrates errors starcheck catches WITHOUT executing the file:
#   - reference to an undefined / non-granted name (`http`)
#   - reassignment of a global (illegal in spec-strict Starlark)
#   - a `while` loop (forbidden unless -while is set)

GREETING = "hi"
GREETING = "bye"          # error: cannot reassign global GREETING

def fetch(url):
    return http.get(url)  # error: undefined: http (unless -predeclared=http)

def spin():
    while True:           # error: while loops are not allowed (unless -while)
        pass
