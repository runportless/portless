# Portless guides

Learn Portless by running Store, a small application with a checkout page,
two databases, and a cache. Each guide follows a task in the dashboard, with
screenshots captured from the running application.

Start with [Run your first application](run-application.md). The other guides
use that same setup and can be followed independently.

| Guide | What you will do |
| --- | --- |
| [Run your first application](run-application.md) | Start Store, create an order, and stop and resume the environment. |
| [Debug checkout with VS Code](debug-checkout-vscode.md) | Start debug mode in Portless, attach VS Code, and inspect a live request at a breakpoint. |
| [Manage an individual service](manage-service.md) | Check health, follow logs, and restart one service. |
| [Understand your application's dependencies](dependencies.md) | Read the topology and follow traffic on a specific connection. |
| [Investigate a failed request](investigate-failed-request.md) | Follow a rejected checkout to inventory's out-of-stock response. |
| [Inspect database and cache activity](database-and-cache.md) | Read SQL results and compare a cache miss with a hit. |
| [Edit and replay an HTTP request](replay-http.md) | Change a captured request and compare the original and replayed responses. |
| [Mock a dependency](mock-dependency.md) | Preview and enable an out-of-stock response without changing inventory data. |
| [Test a slow dependency](test-failures.md) | Add a delay to one connection, inspect its effect, and disable it. |
| [Record a reproduction](record-reproduction.md) | Capture a named session and export it. |
| [Create and switch environments](environments.md) | Clone configuration, run a second environment, and inspect its checkout. |

The screenshots use **store-guides/local**. Your project name, timestamps,
process IDs, debugger ports, and order IDs may differ. Use the values shown in
your own dashboard. All actions apply to the environment named in its header.

For other sample applications, see [Examples](../examples/README.md). For every
CLI option, see the [command reference](../portless-cli/COMMANDS.md).
