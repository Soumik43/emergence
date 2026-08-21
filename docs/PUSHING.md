# Pushing this repo (setup note)

This machine's `gh` and git credentials belong to a work GitHub account
(`soumik-xflowpay`), but this repo lives under the personal account `Soumik43`. Rather
than switch the global auth and disturb work access, the repo is configured to
authenticate on its own.

## One-time setup

1. Create a fine-grained personal access token on the **Soumik43** account:
   <https://github.com/settings/personal-access-tokens/new>
   - Repository access: only `Soumik43/emergence`
   - Permissions: **Contents → Read and write**
   - (A classic token with the `repo` scope works too.)

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
