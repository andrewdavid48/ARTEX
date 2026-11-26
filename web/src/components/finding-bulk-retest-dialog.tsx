"use client";

import * as React from "react";

import { RotateCcwIcon } from "lucide-react";
import { toast } from "sonner";

import { Button } from "@/components/ui/button";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import { Field, FieldDescription, FieldGroup, FieldLabel } from "@/components/ui/field";
import { Spinner } from "@/components/ui/spinner";
import { Textarea } from "@/components/ui/textarea";
import { t } from "@/i18n";
import { api } from "@/lib/api";
import type { FindingRetest } from "@/lib/types";

// How many retest starts are in flight at once. Each started retest then runs
// as its own agent conversation on the server.
const START_CONCURRENCY = 2;

interface FindingBulkRetestDialogProps {
  findingIds: string[];
  onClose: () => void;
  onStarted: (findingId: string, retest: FindingRetest) => void;
  onDone: () => void;
}

// Starts one retest per selected finding through the existing per-finding API.
// Findings that already have a retest running are skipped by the server.
export function FindingBulkRetestDialog({ findingIds, onClose, onStarted, onDone }: FindingBulkRetestDialogProps) {
  const notesId = React.useId();
  const [notes, setNotes] = React.useState("");
  const [progress, setProgress] = React.useState<number | null>(null);
  const submitLock = React.useRef(false);
  const submitting = progress !== null;
  const count = findingIds.length;

  async function start() {
    if (submitLock.current) return;
    submitLock.current = true;
    setProgress(0);
    const note = notes.trim();
    let started = 0;
    let skipped = 0;
    let done = 0;
    const failures: string[] = [];
    const queue = [...findingIds];
    const worker = async () => {
      for (let id = queue.shift(); id !== undefined; id = queue.shift()) {
        try {
          const r = await api.startFindingRetest(id, note);
          if (r.created) {
            started++;
            onStarted(id, r.retest);
          } else {
            skipped++;
          }
        } catch (e) {
          failures.push(`#${id}: ${(e as Error).message}`);
        }
        setProgress(++done);
      }
    };
    await Promise.all(Array.from({ length: Math.min(START_CONCURRENCY, count) }, worker));
    submitLock.current = false;
    const summary = t("Started {started}, skipped {skipped} already running, {failed} failed", {
      started,
      skipped,
      failed: failures.length,
    });
    if (failures.length > 0) toast.error(`${summary}. ${failures[0]}`);
    else toast.success(summary);
    onDone();
    onClose();
  }

  return (
    <Dialog open onOpenChange={(open) => !open && !submitLock.current && onClose()}>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>{t("Retest {count} findings", { count })}</DialogTitle>
          <DialogDescription>
            {t(
              'Each finding is retested by the Retest Agent in its own session. A retest that confirms the fix sets the finding to "Fixed"; other verdicts keep the current status. Findings already being retested are skipped.',
            )}
          </DialogDescription>
        </DialogHeader>
        <p className="rounded-md border border-amber-500/40 bg-amber-500/10 px-3 py-2 text-sm text-amber-600 dark:text-amber-400">
          {t("This starts up to {count} agent sessions that run at the same time and each use LLM tokens.", { count })}
        </p>
        <FieldGroup>
          <Field data-disabled={submitting}>
            <FieldLabel htmlFor={notesId}>{t("Notes (optional)")}</FieldLabel>
            <Textarea
              id={notesId}
              value={notes}
              maxLength={4000}
              rows={4}
              disabled={submitting}
              onChange={(e) => setNotes(e.target.value)}
              placeholder={t("e.g. verify the original endpoint with the original test account; fix version is v2.")}
            />
            <FieldDescription>{t("The same notes are sent with every retest.")}</FieldDescription>
          </Field>
        </FieldGroup>
        <DialogFooter>
          <Button variant="outline" disabled={submitting} onClick={onClose}>
            {t("Cancel")}
          </Button>
          <Button disabled={submitting || count === 0} onClick={() => void start()}>
            {submitting ? <Spinner data-icon="inline-start" /> : <RotateCcwIcon data-icon="inline-start" />}
            {submitting
              ? t("Starting {done}/{count}…", { done: progress, count })
              : t("Start {count} retests", { count })}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
