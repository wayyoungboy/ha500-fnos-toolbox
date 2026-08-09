import assert from "node:assert/strict";
import test from "node:test";

import {
  buildFanCurveChart,
  calculateDutyPercent,
  policySectionsForMode,
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
