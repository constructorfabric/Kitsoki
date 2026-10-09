def main(ctx):
    return ctx.host.call("host.punch.verify", {
        "state_path": ctx.inputs["state_path"],
        "item_id": ctx.inputs["item_id"],
    })
