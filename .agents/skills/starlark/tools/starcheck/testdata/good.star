# A well-formed, spec-strict module. Resolves cleanly with no predeclared names
# (it only uses universe builtins) under `starcheck testdata/good.star`.

NAMES = ["ada", "alan", "grace"]

def greet(name):
    return "hello, " + name

def greet_all(names):
    return [greet(n) for n in names]

GREETINGS = greet_all(NAMES)
