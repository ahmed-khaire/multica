# Observer Swarm Deployment Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Deploy Multica to the existing Docker Swarm as the `observer` stack at `https://observer.edget.co`.

**Architecture:** Build two immutable images: the existing Go backend/CLI image and a new production Next.js web image. Deploy them with a private pgvector Postgres service behind the existing `traefik-public` network, using a transient env file generated from the current Multica `.env` with production URL overrides.

**Tech Stack:** Docker, Docker Swarm, Traefik, PostgreSQL pgvector, Go backend, Next.js 16, pnpm, DigitalOcean Container Registry.

---

## File Structure

- Modify `apps/web/next.config.ts` so production builds emit a standalone Next.js server and trace workspace package files correctly.
- Modify root `Dockerfile` by adding Node-based `web-builder` and `web-runtime` targets while preserving the existing backend runtime target as the default final stage.
- Create root `.dockerignore` to prevent local `.env`, build outputs, and dependency directories from entering Docker build contexts.
- Create `/mnt/data/projects/swarm-setup/manager/apps/observer/stack.yml` as the canonical Swarm stack.
- Create `/mnt/data/projects/swarm-setup/scripts/deploy-observer-stack.sh` to copy the stack to the manager, generate `/opt/swarm/secrets/observer.env` from the current Multica `.env`, deploy with supplied image digests, and shred plaintext secrets.

## Task 1: Web Production Image

**Files:**
- Modify: `apps/web/next.config.ts`
- Modify: `Dockerfile`
- Create: `.dockerignore`

- [ ] Add `output: "standalone"` and `outputFileTracingRoot: resolve(__dirname, "../..")` to the Next.js config.
- [ ] Add Docker build targets:
  - `web-builder`: Node 22 Alpine, pnpm 10.28.2, frozen workspace install, `pnpm --filter @multica/web build`.
  - `web-runtime`: Node 22 Alpine, non-root user, copies `.next/standalone`, `.next/static`, and `public`, exposes `3000`, and runs `node apps/web/server.js`.
- [ ] Keep the existing backend image final stage unchanged so `docker build .` still produces the Go API image containing `server`, `migrate`, and `multica`.
- [ ] Run:

```bash
docker build --target web-runtime \
  --build-arg NEXT_PUBLIC_API_URL=https://observer.edget.co \
  --build-arg NEXT_PUBLIC_WS_URL=wss://observer.edget.co/ws \
  --build-arg REMOTE_API_URL=http://api:8080 \
  -t multica-web:test .
```

Expected: build exits `0`.

## Task 2: Observer Stack And Deploy Script

**Files:**
- Create: `/mnt/data/projects/swarm-setup/manager/apps/observer/stack.yml`
- Create: `/mnt/data/projects/swarm-setup/scripts/deploy-observer-stack.sh`

- [ ] Create a three-service stack:
  - `postgres`: `pgvector/pgvector:pg17`, `observer-postgres-data` volume, private encrypted overlay network, storage placement on `node.labels.storage-size == large`.
  - `api`: `${OBSERVER_API_IMAGE}`, `env_file: /opt/swarm/secrets/observer.env`, command `./migrate up && ./server`, port `8080`, attached to private and `traefik-public` networks.
  - `web`: `${OBSERVER_WEB_IMAGE}`, public production env values, port `3000`, attached to private and `traefik-public` networks.
- [ ] Add Traefik labels:
  - API router: host `observer.edget.co`, path prefixes `/api`, `/auth`, `/ws`, `/health`, `/v1`, priority `100`, service port `8080`.
  - Web router: host `observer.edget.co`, priority `1`, service port `3000`.
- [ ] Add deploy script usage:

```bash
scripts/deploy-observer-stack.sh \
  "$OBSERVER_API_DIGEST" \
  "$OBSERVER_WEB_DIGEST" \
  /mnt/data/projects/observer/multica/.env
```

- [ ] In the deploy script, filter local-only URL keys out of the source env and append production values:

```text
PORT=8080
DATABASE_URL=postgres://$(url_encode "$POSTGRES_USER"):$(url_encode "$POSTGRES_PASSWORD")@postgres:5432/$(url_encode "$POSTGRES_DB")?sslmode=disable
FRONTEND_ORIGIN=https://observer.edget.co
MULTICA_APP_URL=https://observer.edget.co
MULTICA_SERVER_URL=https://observer.edget.co
MULTICA_GATEWAY_BASE_URL=https://observer.edget.co
NEXT_PUBLIC_API_URL=https://observer.edget.co
NEXT_PUBLIC_WS_URL=wss://observer.edget.co/ws
GOOGLE_REDIRECT_URI=https://observer.edget.co/auth/callback
REMOTE_API_URL=http://api:8080
```

Expected: no plaintext env files are committed; the manager copy is shredded after deploy.

## Task 3: Build And Push Images

**Files:**
- No source changes.

- [ ] Compute the image tag:

```bash
TAG="$(git rev-parse --short HEAD)-$(date +%Y%m%d%H%M%S)"
```

- [ ] Build and push backend:

```bash
docker build \
  --build-arg VERSION="$TAG" \
  --build-arg COMMIT="$(git rev-parse --short HEAD)" \
  -t "registry.digitalocean.com/edget/observer-api:$TAG" .
docker push "registry.digitalocean.com/edget/observer-api:$TAG"
```

- [ ] Build and push web:

```bash
docker build --target web-runtime \
  --build-arg NEXT_PUBLIC_API_URL=https://observer.edget.co \
  --build-arg NEXT_PUBLIC_WS_URL=wss://observer.edget.co/ws \
  --build-arg REMOTE_API_URL=http://api:8080 \
  -t "registry.digitalocean.com/edget/observer-web:$TAG" .
docker push "registry.digitalocean.com/edget/observer-web:$TAG"
```

- [ ] Resolve both pushed tags to immutable repo digests with `docker image inspect --format '{{index .RepoDigests 0}}'`.

Expected: both digest refs are non-empty and include `@sha256:`.

## Task 4: Deploy To Swarm

**Files:**
- No source changes.

- [ ] Deploy:

```bash
/mnt/data/projects/swarm-setup/scripts/deploy-observer-stack.sh \
  "$OBSERVER_API_DIGEST" \
  "$OBSERVER_WEB_DIGEST" \
  /mnt/data/projects/observer/multica/.env
```

- [ ] Confirm services are running:

```bash
ssh -i ~/.ssh/frail-flatworm-qs8ogksg8oc8c0kkkko008kc root@72.61.254.249 \
  "docker stack services observer && docker service ps observer_api --no-trunc && docker service ps observer_web --no-trunc && docker service ps observer_postgres --no-trunc"
```

Expected: each service has `1/1` replicas and current tasks are running.

## Task 5: Public Verification

**Files:**
- No source changes.

- [ ] Verify HTTP/TLS:

```bash
curl -fsS https://observer.edget.co/health
curl -fsS -I https://observer.edget.co/
curl -fsS -I https://observer.edget.co/login
```

Expected: health returns `{"status":"ok"}` and pages return `2xx`.

- [ ] Verify browser runtime with Playwright:

```bash
node <<'JS'
const { chromium } = require("playwright");

(async () => {
  const browser = await chromium.launch({ headless: true });
  const page = await browser.newPage();
  const consoleErrors = [];
  const pageErrors = [];
  const localhostRequests = [];

  page.on("console", (message) => {
    if (message.type() === "error") consoleErrors.push(message.text());
  });
  page.on("pageerror", (error) => pageErrors.push(error.message));
  page.on("request", (request) => {
    const url = request.url();
    if (url.includes("localhost") || url.includes("127.0.0.1")) {
      localhostRequests.push(url);
    }
  });

  await page.goto("https://observer.edget.co/login", { waitUntil: "networkidle" });
  await page.getByText("Sign in to Multica").waitFor({ timeout: 10000 });

  await browser.close();

  if (consoleErrors.length || pageErrors.length || localhostRequests.length) {
    console.error(JSON.stringify({ consoleErrors, pageErrors, localhostRequests }, null, 2));
    process.exit(1);
  }
  console.log("observer public browser smoke passed");
})();
JS
```

The script checks that the app loads, login screen renders, no browser console/page errors occur on load, and no browser requests target localhost.

- [ ] Verify the backend image contains the CLI:

```bash
docker run --rm "$OBSERVER_API_DIGEST" ./multica version
```

Expected: command prints a Multica version string.

- [ ] Verify generated gateway URLs after login/API setup or by inspecting stack env:

```text
MULTICA_GATEWAY_BASE_URL=https://observer.edget.co
```

Expected: `multica gateway key` will generate `OPENAI_BASE_URL=https://observer.edget.co/v1` and `ANTHROPIC_BASE_URL=https://observer.edget.co`.
