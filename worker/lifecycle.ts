type WaitUntilContext = { waitUntil(promise: Promise<unknown>): void };
type Logger = Pick<Console, "error">;

type LifecycleDependencies<Env, Context extends WaitUntilContext> = {
  handleRequest(request: Request, env: Env, ctx: Context): Promise<Response>;
  startup(env: Env): Promise<unknown>;
  scheduled(controller: { cron: string }, env: Env): Promise<unknown>;
  logger?: Logger;
};

export function createWorkerLifecycle<Env, Context extends WaitUntilContext>(
  dependencies: LifecycleDependencies<Env, Context>,
) {
  const logger = dependencies.logger ?? console;
  let startupJob: Promise<void> | null = null;

  function run(operation: () => Promise<unknown>, trigger: "startup" | "scheduled") {
    return operation().then(
      () => undefined,
      (error) => {
        logger.error("[worker-lifecycle]", {
          event: "background_failed",
          trigger,
          error: error instanceof Error ? error.message : "市场收益预热失败",
        });
      },
    );
  }

  return {
    async fetch(request: Request, env: Env, ctx: Context) {
      startupJob ??= run(() => dependencies.startup(env), "startup");
      ctx.waitUntil(startupJob);
      return dependencies.handleRequest(request, env, ctx);
    },
    scheduled(controller: { cron: string }, env: Env, ctx: Context) {
      ctx.waitUntil(run(() => dependencies.scheduled(controller, env), "scheduled"));
    },
  };
}
