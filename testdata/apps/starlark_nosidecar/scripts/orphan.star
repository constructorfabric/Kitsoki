# orphan.star — deliberately has NO sidecar (orphan.star.yaml is absent) so the
# loader fails the app at load time. Used by the missing-sidecar load-error test.

def main(ctx):
    return {"ok": True}
