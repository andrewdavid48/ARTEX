"use client";

import * as React from "react";

import Link from "next/link";

import { FolderOpenIcon, GitBranchIcon, Loader2Icon, RefreshCwIcon, Trash2Icon } from "lucide-react";
import { toast } from "sonner";

import { Button } from "@/components/ui/button";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { Input } from "@/components/ui/input";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import { intlLocale, t } from "@/i18n";
import { api, type RepoInfo } from "@/lib/api";

// Server-side GitHub clones. The server runs git with the token from Settings, so
// the token never reaches the agent; clones land in the workspace under repos/.
export function ReposPanel({ onOpen, onChanged }: { onOpen: (path: string) => void; onChanged: () => void }) {
  const [repos, setRepos] = React.useState<RepoInfo[]>([]);
  const [tokenSet, setTokenSet] = React.useState(false);
  const [url, setUrl] = React.useState("");
  const [ref, setRef] = React.useState("");
  const [cloning, setCloning] = React.useState(false);
  const [busy, setBusy] = React.useState<string | null>(null);

  const load = React.useCallback(() => {
    api
      .reposList()
      .then((r) => {
        setRepos(r.repos);
        setTokenSet(r.token_set);
      })
      .catch((e) => toast.error(t("Failed to list repositories: {error}", { error: (e as Error).message })));
  }, []);

  React.useEffect(() => {
    load();
  }, [load]);

  const clone = (e: React.FormEvent) => {
    e.preventDefault();
    setCloning(true);
    api
      .repoClone(url.trim(), ref.trim())
      .then((r) => {
        toast.success(t("Cloned {name}", { name: r.name }));
        setUrl("");
        setRef("");
        load();
        onChanged();
      })
      .catch((err) => toast.error(t("Clone failed: {error}", { error: (err as Error).message })))
      .finally(() => setCloning(false));
  };

  const pull = (name: string) => {
    setBusy(name);
    api
      .repoPull(name)
      .then((r) => {
        toast.success(t("Updated {name} to {commit}", { name: r.name, commit: r.commit.slice(0, 7) }));
        load();
        onChanged();
      })
      .catch((err) => toast.error(t("Pull failed: {error}", { error: (err as Error).message })))
      .finally(() => setBusy(null));
  };

  const remove = (name: string) => {
    if (!window.confirm(t("Delete the clone {name}? The files are removed from the workspace.", { name }))) return;
    setBusy(name);
    api
      .repoDelete(name)
      .then(() => {
        toast.success(t("Deleted"));
        load();
        onChanged();
      })
      .catch((err) => toast.error(t("Delete failed: {error}", { error: (err as Error).message })))
      .finally(() => setBusy(null));
  };

  return (
    <Card>
      <CardHeader>
        <CardTitle className="flex items-center gap-2 text-base">
          <GitBranchIcon className="size-4" />
          {t("Repositories")}
        </CardTitle>
        <CardDescription>
          {t(
            "Clone a GitHub repository into the workspace for white-box review. The server runs git; the token is never shown to agents.",
          )}{" "}
          {tokenSet ? (
            <span className="text-emerald-500">{t("GitHub token configured.")}</span>
          ) : (
            <>
              {t("No GitHub token: only public repositories will clone.")}{" "}
              <Link href="/system/settings" className="underline">
                {t("Set one in Settings")}
              </Link>
            </>
          )}
        </CardDescription>
      </CardHeader>
      <CardContent className="flex flex-col gap-4">
        <form onSubmit={clone} className="flex flex-wrap items-center gap-2">
          <Input
            className="min-w-0 flex-[3_1_18rem]"
            placeholder="https://github.com/owner/repo"
            value={url}
            disabled={cloning}
            onChange={(e) => setUrl(e.target.value)}
            aria-label={t("Repository URL")}
          />
          <Input
            className="min-w-0 flex-[1_1_8rem]"
            placeholder={t("Branch or tag (optional)")}
            value={ref}
            disabled={cloning}
            onChange={(e) => setRef(e.target.value)}
            aria-label={t("Branch or tag")}
          />
          <Button type="submit" disabled={cloning || url.trim() === ""}>
            {cloning && <Loader2Icon className="size-4 animate-spin" />}
            {cloning ? t("Cloning…") : t("Clone")}
          </Button>
        </form>
        {repos.length > 0 && (
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead>{t("Repository")}</TableHead>
                <TableHead>{t("Branch")}</TableHead>
                <TableHead>{t("Commit")}</TableHead>
                <TableHead>{t("Updated")}</TableHead>
                <TableHead className="text-right">{t("Actions")}</TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {repos.map((r) => (
                <TableRow key={r.name}>
                  <TableCell className="font-mono text-xs">
                    {r.url.replace(/^https:\/\/github\.com\//, "").replace(/\.git$/, "")}
                  </TableCell>
                  <TableCell className="font-mono text-xs">{r.ref}</TableCell>
                  <TableCell className="font-mono text-xs">{r.commit.slice(0, 7)}</TableCell>
                  <TableCell className="text-xs text-muted-foreground">
                    {r.updated_at ? new Date(r.updated_at).toLocaleString(intlLocale) : "—"}
                  </TableCell>
                  <TableCell>
                    <div className="flex items-center justify-end gap-1">
                      <Button
                        variant="ghost"
                        size="icon"
                        className="size-8"
                        title={t("Open in file manager")}
                        onClick={() => onOpen(r.path)}
                      >
                        <FolderOpenIcon className="size-4" />
                      </Button>
                      <Button
                        variant="ghost"
                        size="icon"
                        className="size-8"
                        title={t("Pull latest")}
                        disabled={busy === r.name}
                        onClick={() => pull(r.name)}
                      >
                        <RefreshCwIcon className={busy === r.name ? "size-4 animate-spin" : "size-4"} />
                      </Button>
                      <Button
                        variant="ghost"
                        size="icon"
                        className="size-8 text-destructive"
                        title={t("Delete clone")}
                        disabled={busy === r.name}
                        onClick={() => remove(r.name)}
                      >
                        <Trash2Icon className="size-4" />
                      </Button>
                    </div>
                  </TableCell>
                </TableRow>
              ))}
            </TableBody>
          </Table>
        )}
      </CardContent>
    </Card>
  );
}
