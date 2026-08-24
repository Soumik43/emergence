# Pushing this repo (setup note)

This machine's `gh` and git credentials belong to a work GitHub account
(`soumik-xflowpay`), but this repo lives under the personal account `Soumik43`. Rather
than switch the global auth and disturb work access, the repo is configured to
authenticate on its own.

## One-time setup

> **Order matters, and getting it wrong costs a confusing detour.** An earlier version of
> this doc had steps 1 and 2 the other way round. A fine-grained token scoped to "only
> select repositories" can only select repositories that *already exist*, so a token
> created first cannot be scoped to a repo created second — and the resulting push fails
> with `Repository not found`, which reads like the repo is missing when it is really the
> token that cannot see it. GitHub returns 404 rather than 403 for private repos so as not
> to leak their existence.

1. **Create the repository first**, on the **Soumik43** account:
   <https://github.com/new> — name `emergence`, **Private**, and add no README, `.gitignore`
   or licence (this repo already has them, and an initial commit on the remote would force
   a merge).

   This can't be done with a fine-grained token unless it carries
   *Account permissions → Administration: write*, which they don't have by default, so the
   browser is the path of least resistance.

2. **Then** create the token: <https://github.com/settings/personal-access-tokens/new>
   - Repository access: **Only select repositories** → `emergence`
   - Permissions → Repository permissions → **Contents: Read and write**
     (Metadata: Read-only is added automatically and is also required)
   - A classic token with the `repo` scope avoids the ordering problem entirely, since it
     is not scoped per-repository.

2. Save it outside the repo, readable only by you:

   ```bash
   printf '%s' 'github_pat_...' > ~/.emergence-pat
   chmod 600 ~/.emergence-pat
   ```

3. Push:

   ```bash
   git push -u origin main
   ```

## How it works

A repo-local credential helper, set with `git config --local`, so nothing here touches the
global config, the macOS keychain, or the `gh` session:

```
credential.helper = !f() { test "$1" = get && echo "username=Soumik43" \
    && echo "password=$(cat "$HOME/.emergence-pat")"; }; f
```

The token is never written into the repo, the remote URL, or the git config — only the
path to it is. `.gitignore` also excludes `*.pat` and `.env` as a second line of defence.

To undo: `git config --local --unset credential.helper`.

## Troubleshooting

**`remote: Repository not found`** — two different causes, same message:

1. The repo genuinely doesn't exist yet. Check: `curl -s -o /dev/null -w '%{http_code}\n'
   https://api.github.com/users/Soumik43` returns 200 for the user, but the repo is a
   separate thing — `git remote add` only writes a local pointer, it creates nothing.
2. The repo exists and is private, but the token can't see it. This is the one that looks
   like cause 1. Test it directly:

   ```bash
   curl -s -H "Authorization: Bearer $(cat ~/.emergence-pat)" \
     https://api.github.com/repos/Soumik43/emergence | head -3
   ```

   `Not Found` here, while the repo loads fine in the browser, means the token's
   repository access doesn't include it. Fix at
   <https://github.com/settings/personal-access-tokens> → select the token →
   Repository access → add `emergence` (or switch to All repositories) → Update.

   Editing a fine-grained token's scope **does not change the token string**, so
   `~/.emergence-pat` stays valid and needs no update.

**Check which account a token belongs to**, without printing it:

```bash
curl -s -H "Authorization: Bearer $(cat ~/.emergence-pat)" \
  https://api.github.com/user | grep '"login"'
```

**SSH is not a shortcut here.** The SSH key on this machine
(`~/.ssh/id_ed25519`) is registered to the *work* account — `ssh -T git@github.com`
answers `Hi soumik-xflowpay!` — so it cannot reach a private repo owned by `Soumik43`
either.

## Commit author email

Commits so far use the git global identity, which is the **work** email
(`soumik@xflowpay.com`). GitHub attributes commits by email, so unless that address is
also registered on the `Soumik43` account, the commit history will show the right name
with no avatar or profile link.

Given that commit history is part of what's being graded here, it's worth fixing before
pushing. Set the identity for this repo only:

```bash
git config --local user.email "your-personal@email.com"
```

Then rewrite the existing commits to match (safe — nothing has been pushed yet):

```bash
git rebase --root --exec 'git commit --amend --no-edit --reset-author'
```

## Collaborators

The brief requires adding two collaborators to the private repo:

```bash
# after the repo exists on GitHub, as Soumik43
gh api -X PUT repos/Soumik43/emergence/collaborators/chiragmakkar -f permission=pull
```

`hari@emsoft.com` has to be invited by email through the web UI
(Settings → Collaborators → Add people), since the API takes usernames rather than
addresses.
