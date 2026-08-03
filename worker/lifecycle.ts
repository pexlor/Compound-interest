type WaitUntilContext = { waitUntil(promise: Promise<unknown>): void };
type Logger = Pick<Console, "error">;

type LifecycleDependencies<Env, Context extends WaitUntilContext> = {
  handleRequest(request: Request, env: Env, ctx: Context): Promise<Response>;
  prewarm(env: Env, trigger: "startup" | "scheduled"): Promise<unknown>;
  logger?: Logger;
};

export function createWorkerLifecycle<Env, Context extends WaitUntilContext>(
  dependencies: LifecycleDependencies<Env, Context>,
) {
  const logger = dependencies.logger ?? console;
  let startupWarmup: Promise<void> | null = null;

  function runPrewarm(env: Env, trigger: "startup" | "scheduled") {
    return dependencies.prewarm(env, trigger).then(
      () => undefined,
      (error) => {
        logger.error("[market-return]", {
          event: "prewarm_failed",
          trigger,
          error: error instanceof Error ? error.message : "市场收益预热失败",
        });
      },
    );
  }

  return {
    async fetch(request: Request, env: Env, ctx: Context) {
      startupWarmup ??= runPrewarm(env, "startup");
      ctx.waitUntil(startupWarmup);
      return dependencies.handleRequest(request, env, ctx);
    },
    scheduled(_controller: unknown, env: Env, ctx: Context) {
      ctx.waitUntil(runPrewarm(env, "scheduled"));
    },
  };
}
