import assert from "node:assert/strict";
import { readFile } from "node:fs/promises";
import test from "node:test";

import {
  buildFanCurveChart,
  calculateDutyPercent,
  policySectionsForMode,
  prepareCurveForSubmission,
  shouldRefreshFanEditor,
  temperatureSourceOptions,
} from "../internal/server/public/fan-curve.mjs";

const curve = [
  { temperature_c: 40, duty_percent: 35 },
  { temperature_c: 60, duty_percent: 55 },
  { temperature_c: 75, duty_percent: 75 },
  { temperature_c: 80, duty_percent: 100 },
];

test("fan curve chart renders axes, nodes, current temperature and critical marker", () => {
  const markup = buildFanCurveChart({
    points: curve,
    currentTemperature: 50,
    criticalTemperature: 80,
  });

  assert.match(markup, /<svg[^>]+role="img"/);
  assert.match(markup, /PWM 占空比/);
  assert.match(markup, /CPU 温度/);
  assert.match(markup, /40°C · 35%/);
  assert.match(markup, /80°C · 100%/);
  assert.match(markup, /当前 50\.0°C · 45%/);
  assert.match(markup, /临界 80°C/);
  assert.match(markup, /class="curve-line"/);
});

test("fan duty follows the same clamped linear curve semantics as the controller", () => {
  assert.equal(calculateDutyPercent(curve, 20), 35);
  assert.equal(calculateDutyPercent(curve, 50), 45);
  assert.equal(calculateDutyPercent(curve, 90), 100);
});

test("policy mode only exposes fields relevant to that mode", () => {
  assert.deepEqual(policySectionsForMode("curve"), {
    controlled: true,
    curve: true,
    fixed: false,
  });
  assert.deepEqual(policySectionsForMode("fixed"), {
    controlled: true,
    curve: false,
    fixed: true,
  });
  assert.deepEqual(policySectionsForMode("monitor"), {
    controlled: false,
    curve: false,
    fixed: false,
  });
});

test("fan layout constrains the wide curve canvas inside the mobile page", async () => {
  const stylesheet = await readFile(
    new URL("../internal/server/public/styles.css", import.meta.url),
    "utf8",
  );

  assert.match(stylesheet, /\.fan-layout>\*\{min-width:0\}/);
  assert.match(stylesheet, /\.fan-curve-chart\{[^}]*max-width:100%/);
});

test("background refresh preserves an editor with unsaved changes", () => {
  assert.equal(shouldRefreshFanEditor({ selected: true, dirty: true }), false);
  assert.equal(shouldRefreshFanEditor({ selected: true, dirty: false }), true);
  assert.equal(shouldRefreshFanEditor({ selected: false, dirty: true }), false);
});

test("curve submission uses the same sorted order as its preview", () => {
  assert.deepEqual(
    prepareCurveForSubmission([
      { temperature_c: 80, duty_percent: 100 },
      { temperature_c: 40.5, duty_percent: 35 },
      { temperature_c: 60, duty_percent: 55 },
    ]),
    [
      { temperature_c: 40.5, duty_percent: 35 },
      { temperature_c: 60, duty_percent: 55 },
      { temperature_c: 80, duty_percent: 100 },
    ],
  );
  assert.throws(
    () =>
      prepareCurveForSubmission([
        { temperature_c: 40, duty_percent: 35 },
        { temperature_c: 40, duty_percent: 55 },
      ]),
    /温度节点不能重复/,
  );
});

test("fan editor exposes decimal temperature inputs and selected-state semantics", async () => {
  const application = await readFile(
    new URL("../internal/server/public/app.js", import.meta.url),
    "utf8",
  );

  assert.match(application, /step="any"/);
  assert.match(application, /data-temp type="number" step="any"/);
  assert.doesNotMatch(
    application,
    /id="critical-temperature"[^>]*step="any"/,
  );
  assert.match(application, /aria-pressed=/);
});

test("disk temperature sources are selectable by stable fingerprint without exposing serials", () => {
  const options = temperatureSourceOptions(
    [
      { id: "cpu", kind: "cpu", label: "CPU Package", temperature_c: 54.2, available: true },
      { id: `disk:${"a".repeat(64)}`, kind: "disk", label: "DISK 1 · Example Disk · ID A1B2C3", temperature_c: 40, available: true },
    ],
    `disk:${"a".repeat(64)}`,
  );

  assert.deepEqual(options, [
    { value: "cpu", label: "CPU Package · 54.2°C", currentTemperature: 54.2, available: true },
    { value: `disk:${"a".repeat(64)}`, label: "硬盘 · DISK 1 · Example Disk · ID A1B2C3 · 40.0°C", currentTemperature: 40, available: true },
  ]);
  assert.doesNotMatch(JSON.stringify(options), /PRIVATE-SERIAL/);
});

test("a saved disk source remains visible when the disk temperature is unavailable", () => {
  const missing = `disk:${"b".repeat(64)}`;
  const options = temperatureSourceOptions(
    [{ id: "cpu", kind: "cpu", label: "CPU Package", temperature_c: 52, available: true }],
    missing,
  );

  assert.deepEqual(options.at(-1), {
    value: missing,
    label: "硬盘 · 已保存的硬盘（当前未找到或温度不可用）",
    currentTemperature: null,
    available: false,
  });
});
