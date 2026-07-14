def main(ctx):
    return {"result": ctx.host.call("host.decomposition.update", ctx.inputs)}
