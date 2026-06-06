# Observer Swarm Deployment Design

Date: 2026-06-06

Status: design approved for repo-managed Swarm deployment

## Goal

Deploy the current Multica app to the existing Swarm infrastructure and serve it at:

```text
https://observer.edget.co
```

The deployment should use `/mnt/data/projects/swarm-setup` as the infrastructure source of truth, reuse the current Multica `.env` configuration, and appear in Portainer through Swarm service discovery. The selected deployment path is repo-managed `docker stack deploy`, not Portainer API-managed stack creation, because no Portainer API token or credentials are available in the workspace.

## Selected Approach

Use the existing Swarm manager and `swarm-setup` repository patterns:

1. Add an `observer` app stack under `/mnt/data/projects/swarm-setup/manager/apps/observer/`.
2. Build and push a backend image from Multica's Go Dockerfile.
3. Add and build a production Next.js web image for `apps/web`.
4. Deploy the stack to the Swarm manager with `docker stack deploy`.
5. Attach services to the existing external `traefik-public` overlay network.
6. Route `observer.edget.co` through Traefik to the web and API services.

This keeps the deployment auditable from the repository while still making the stack visible and manageable in Portainer.

## Runtime Services

### `postgres`

Runs `pgvector/pgvector:pg17` for Multica's SQL database. It uses a named Swarm volume and the database name, user, and password from the current Multica `.env` values.

The database is private to the overlay network and is not exposed publicly.

### `api`

Runs the Go backend image built from the repository root Dockerfile. The image includes:

- `server` HTTP API binary
- `migrate` migration binary
- `multica` CLI binary

The service starts by running database migrations, then starts the server on port `8080`. It runs as a single replica for the first deployment to avoid migration races.

### `web`

Runs a production Next.js image for `apps/web` on port `3000`.

Build-time and runtime public URL values are set for the production host:

```text
NEXT_PUBLIC_API_URL=https://observer.edget.co
NEXT_PUBLIC_WS_URL=wss://observer.edget.co/ws
REMOTE_API_URL=http://api:8080
```

`REMOTE_API_URL` keeps server-side Next.js rewrites pointed at the internal backend service while browser-facing URLs point at the public host.

## Routing

One public host is used:

```text
observer.edget.co
```

Traefik routers:

- API router: `Host(observer.edget.co)` plus API path prefixes, service port `8080`
- Web router: `Host(observer.edget.co)`, service port `3000`

The API router has higher priority and matches:

```text
/api
/auth
/ws
/health
/v1
```

`/ws` is included for the realtime WebSocket endpoint. `/v1` is included because Multica has gateway-style API surfaces that should be reachable through the same origin if enabled by the current code and environment.

TLS uses the existing Traefik Let's Encrypt resolver used by the Swarm.

## Environment And Secrets

The current Multica `.env` is the source for deployment values, but plaintext secrets must not be committed to either repository.

The deployment flow creates or uses a transient environment file on the Swarm manager for stack interpolation. Secret values are copied from the current `.env`, used by `docker stack deploy`, and then removed from the manager.

Sensitive values include, but are not limited to:

- `DATABASE_URL`
- `POSTGRES_PASSWORD`
- `JWT_SECRET`
- provider keys
- Google OAuth client secret
- Resend key
- S3 and CloudFront credentials
- gateway secret material

Non-secret public production values are set explicitly:

```text
FRONTEND_ORIGIN=https://observer.edget.co
MULTICA_APP_URL=https://observer.edget.co
MULTICA_SERVER_URL=wss://observer.edget.co/ws
NEXT_PUBLIC_API_URL=https://observer.edget.co
NEXT_PUBLIC_WS_URL=wss://observer.edget.co/ws
GOOGLE_REDIRECT_URI=https://observer.edget.co/auth/callback
```

If the existing `.env` has local-only values such as localhost URLs, the deployment transforms them to the production host for the stack rather than preserving localhost.

`COOKIE_DOMAIN` is copied from the current `.env` as-is unless CloudFront signed cookies are enabled and require an explicit production domain. The current local value is empty, and forcing a synthesized cookie domain would change CloudFront cookie behavior.

## CLI Handling

Multica's CLI is built into the backend Docker image as `/app/multica`. This deployment makes the hosted server available for CLI clients at `https://observer.edget.co`; CLI users should configure their server URL to that host.

This deployment does not add a new browser download page or static binary hosting endpoint for the CLI. Existing CLI distribution paths such as release artifacts, Homebrew, or local builds remain the source for installing the CLI binary.

## Image Registry

Images are built locally and pushed to an authenticated registry already available in the environment. The preferred registry is DigitalOcean Container Registry because local Docker auth is present and existing Swarm services already use DigitalOcean registry images.

The deployed stack should reference immutable image digests after push when practical.

## Placement

The initial deployment is conservative:

- `postgres` placed on a storage-capable Swarm node, preferably the larger storage node.
- `api` and `web` run as single replicas for the first deployment.
- Services attach only to required networks.

Scaling can be added after the first deployment is verified. Backend migration startup should be separated or guarded before running multiple API replicas.

## Verification

After deployment, verify:

```text
https://observer.edget.co
https://observer.edget.co/health
https://observer.edget.co/api/...
wss://observer.edget.co/ws
```

Operational checks:

- `docker stack services observer`
- `docker service logs observer_api`
- `docker service logs observer_web`
- `docker service logs observer_postgres`
- `docker exec` or image inspection confirms the backend image contains `/app/multica`

Browser verification should confirm that the Next.js app loads from the public host and does not call localhost API or WebSocket URLs.

## Rollback

Rollback is stack-level:

1. Redeploy previous image digests if available.
2. If the deployment is not usable, remove the stack with `docker stack rm observer`.
3. Preserve the Postgres volume unless explicitly deleting production data.

Database migrations may not be reversible without data loss. Before future schema-changing deployments, capture the migration set and backup posture explicitly.
