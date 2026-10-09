def main(ctx):
    return ctx.host.call("host.proposal.publish", ctx.inputs)
