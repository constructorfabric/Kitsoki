def main(ctx):
    return {"result": ctx.host.call("host.bakeoff.run", ctx.inputs)}
