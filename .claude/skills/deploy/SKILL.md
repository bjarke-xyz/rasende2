---
name: deploy
description: Deploy rasende2 to production (oci-fra-2) using the devops Ansible deployment script. Use when asked to deploy, ship, or release rasende2.
---

# Deploy rasende2

Deployment lives in the separate `devops` repo. The script builds a Docker
image from the local working tree at
`/Users/bjarketobiesen/src/bjarke-xyz/rasende2`, uploads it to `oci-fra-2`,
and runs the Ansible playbook.

## Before deploying

The image is built from the working tree, so uncommitted changes go to
production. Before continuing:

1. Run `git -C /Users/bjarketobiesen/src/bjarke-xyz/rasende2 status --short`.
   If the tree is dirty, explain what is uncommitted and ask before deploying.
2. Run `make -C /Users/bjarketobiesen/src/bjarke-xyz/rasende2 test`. Do not
   deploy over failing tests without the user's approval.
3. If this change added an environment variable, check that it is configured in
   `playbooks/apps/roles/rasende2/templates/secrets.j2` in devops before
   deploying.

## Deploying

Run the deployment in the background because the multi-architecture Docker
build, image upload over SSH, and Ansible playbook can take several minutes:

```bash
cd /Users/bjarketobiesen/src/bjarke-xyz/devops/ansible && ./run/deploy.sh rasende2
```

The script defaults to `oci-fra-2`, where rasende2 runs. Its vault password is
read from `~/.ansible_vault_pass`; if the file is missing, the script prompts
for it. Do not leave a prompted deploy hanging: stop it and tell the user.

Success ends with `✅ Deployment complete: rasende2`. Report any other result
verbatim rather than claiming the app deployed.

## Verifying

The Ansible playbook reports that the container started, which does not
guarantee the app finished booting. Check its state and recent logs on the host:

```bash
ssh oci-fra-2 '
docker inspect -f "{{.State.Status}} exit={{.State.ExitCode}} restarts={{.RestartCount}}" rasende2-rasende2-1
docker logs --since 10m rasende2-rasende2-1 2>&1 | tail -30
'
```

Confirm the container is running without a growing restart count and its logs
show the web server listening on port 8080. Then check
`https://rasende.bjarke.xyz/health` returns `{"status":"ok"}`.

The container is `rasende2-rasende2-1`, served behind Caddy on
`oci-fra-2`. Its database is `/opt/data/rasende2/rasende2.db` on the host;
database backups are managed by the devops repository.
