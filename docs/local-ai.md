# Local models and private-data workflows

[README](../README.md)

TSLink can give an existing local model HTTP API a named HTTPS address inside your
Tailscale network. A permitted device can then use that API from an application or agent.
The model backend runs inference; TSLink forwards its HTTP traffic.

## Before you start

- Complete [TSLink installation and tailnet setup](getting-started.md). The publishing
  host needs a working TSLink daemon, and the receiving device needs Tailscale and permission
  to reach the new service. Authorize each new node when prompted; device approval may also apply.
- Run Ollama separately on the publishing host at `localhost:11434`. Choose a downloaded
  local model if you want inference on that machine. Ollama can also use cloud models.
- The shell examples use **bash or zsh** and `curl`. See [platform support](platforms.md)
  for Windows requirements.

## 1. Check the existing API locally

On the machine running Ollama:

```bash
curl http://localhost:11434/api/tags
```

This [Ollama endpoint](https://docs.ollama.com/api/tags) lists available models.
An empty list means you still need to install a model before requesting local inference.
If the API is unreachable locally, start or fix the backend before registering it with TSLink.

## 2. Register the model service

```bash
tslink add model --proxy localhost:11434
tslink url model --wait
```

Complete Tailscale node enrollment if prompted. Use the exact returned HTTPS URL rather
than constructing a hostname. `add` creates a durable node by default.

To restrict this HTTP API to your Tailscale login, replace the email below with your actual
login and use this registration command instead:

```bash
tslink add model --proxy localhost:11434 --allow you@example.com
```

The tailnet must also permit the connection. Same-name `add` replaces all service settings,
so repeat the intended flags when changing it. The ordinary model service stays private;
do not enable Funnel for this private-data workflow.

## 3. Connect from a permitted device

On a receiving device, replace the placeholder with the URL printed by TSLink:

```bash
MODEL_URL='PASTE_THE_EXACT_URL_RETURNED_BY_TSLINK'
curl "$MODEL_URL/api/tags"
```

Use that same address in your client. These are **configuration templates**, not live URLs:

| Client/API | Address to configure | Request route |
|---|---|---|
| Ollama client asking for a server URL | `<returned URL>` | The client selects `/api/...` routes |
| Native Ollama API client asking for an API base | `<returned URL>/api` | `/chat`, `/generate`, `/tags` |
| OpenAI-compatible client asking for `baseURL` | `<returned URL>/v1` | `/chat/completions` |

Choose a model name available on the backend. Ollama provides a
[subset of OpenAI API compatibility](https://docs.ollama.com/api/openai-compatibility);
check the routes and options your client uses. Local Ollama requests do not require an
Ollama API key; a client's required key field does not replace Tailscale access controls.

For a native chat request, replace the model placeholder below with an installed local model:

```bash
curl "$MODEL_URL/api/chat" \
  -H 'Content-Type: application/json' \
  -d '{"model":"REPLACE_WITH_INSTALLED_LOCAL_MODEL","messages":[{"role":"user","content":"Reply with one short greeting."}],"stream":false}'
```

This small request checks the configured inference path without submitting private data.
Ollama's native API can [stream newline-delimited JSON](https://docs.ollama.com/api/streaming);
the example disables streaming to make the response easy to inspect.

<a id="pair-a-model-api-with-a-web-ui"></a>

## Pair a model API with a web UI

Use this template as an alternative to the manual registration above: its model API is named
`ollama`, while the manual example uses `model`. Choose one route for that backend to avoid duplicate nodes.

The shipped `local-ai-suite` template configures two separate TSLink service nodes:

| Service name | HTTP backend | Role |
|---|---|---|
| `ollama` | `localhost:11434` | Model API |
| `open-webui` | `localhost:8080` | Browser application |

Install, configure, and start both applications separately. Configure Open WebUI's model
connection for your deployment; the template registers access routes only.

Preview the plan first:

```bash
tslink template apply local-ai-suite
```

Register missing services after reviewing it:

```bash
tslink template apply local-ai-suite --yes
tslink url ollama --wait
tslink url open-webui --wait
```

Templates preserve existing service entries. `local-web` and `dev-suite` are also available;
`tslink template list` shows the built-ins.

## A practical private-data arrangement

<picture>
  <source media="(max-width: 600px) and (prefers-color-scheme: dark)" srcset="assets/local-ai-flow-dark-mobile.svg">
  <source media="(max-width: 600px)" srcset="assets/local-ai-flow-light-mobile.svg">
  <source media="(prefers-color-scheme: dark)" srcset="assets/local-ai-flow-dark.svg">
  <img src="assets/local-ai-flow-light.svg" alt="Example private-data workflow: a permitted client reaches a local app through TSLink; the app reads local documents and calls a local model API, then returns a response." width="960">
</picture>

Let an authorized device reach your local application through its **App** node. The
application reads local documents or a local database and calls a downloaded model on the
publishing machine. **Docs**, **Database**, and **Model** can have separate named nodes when
you also need to access them directly. All are in the same tailnet, with distinct service identities.

The application implements document loading, embeddings, retrieval, and RAG. Selecting a
local model and local data stores can keep those operations on your own host. Configure
the application's model selection, client/backend logging, external tools, and outbound
requests according to the sensitivity of your data; a localhost API can still use a cloud model.

TSLink's MCP endpoint supplies tools to manage services and sharing. Configure an agent's
inference connection separately using the model API address above. Remote/cloud agents
also need a route into your tailnet and may process the data they receive on their own host.

## HTTP behavior and operation

The proxy forwards request paths and bodies to the backend and supports response flushing
for streaming. It rewrites forwarding and Tailscale identity headers and filters hop-by-hop
headers. Model protocol compatibility comes from your backend and client.

The server's default request limits are **32 MiB for bodies**, **64 KiB for headers**, and a
**30-second request read timeout**. Large uploads need to fit those limits. Generation behavior
also depends on the backend and client. Keep both TSLink and the model/application processes
running; see [daemon lifecycle](daemon-lifecycle.md).

For URL and setup checks, use `tslink status --urls --json` and `tslink doctor --json`.
For per-service identity restrictions and TCP boundaries, see [sharing](sharing.md).

Primary API reference: [Ollama API introduction](https://docs.ollama.com/api/introduction).
