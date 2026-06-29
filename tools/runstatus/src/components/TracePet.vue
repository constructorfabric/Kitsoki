<!--
  TracePet.vue — a small decorative pet that lives along the bottom of the trace
  column (VS-Code-Pets / tamagotchi style). Purely ambient: it wanders left and
  right, blinks, occasionally drops a poop, and now and then a snack appears and
  it walks over to eat it. No coupling to session/trace state — it just keeps you
  company while you watch a run.

  Implementation per the design (docs/proposals/trace-pet): one self-contained
  component, inline SVG + CSS keyframes for the sprite, a tiny JS behaviour loop
  for wandering/poop/food. Zero dependencies, zero network. Opt-in: the parent
  only mounts this when the pet is enabled. Absolutely positioned over the column
  floor so it never displaces a trace row.
-->
<template>
  <div class="pet" aria-hidden="true" data-testid="trace-pet">
    <!-- droppings the pet has left behind -->
    <span
      v-for="p in poops"
      :key="p.id"
      class="pet__poop"
      :class="{ 'pet__poop--fading': p.fading }"
      :style="{ left: p.x + '%' }"
    >💩</span>

    <!-- a snack waiting to be eaten -->
    <span
      v-if="food"
      class="pet__food"
      :class="{ 'pet__food--eaten': food.eaten }"
      :style="{ left: food.x + '%' }"
    >🍎</span>

    <!-- the pet itself -->
    <div
      class="pet__sprite"
      :class="{ 'pet__sprite--walking': walking, 'pet__sprite--eating': eating }"
      :style="{ left: x + '%', transform: `translateX(-50%) scaleX(${facing})` }"
    >
      <svg width="34" height="34" viewBox="0 0 40 40">
        <ellipse class="pet__shadow" cx="20" cy="35" rx="11" ry="3" />
        <path
          class="pet__body"
          d="M20 8 C29 8 33 15 33 23 C33 31 27 34 20 34 C13 34 7 31 7 23 C7 15 11 8 20 8 Z"
        />
        <path
          class="pet__sheen"
          d="M20 8 C29 8 33 15 33 23 C33 27 31 30 28 32 C30 28 30 22 27 18 C24 14 20 13 20 13 Z"
        />
        <ellipse class="pet__eye" cx="15" cy="22" rx="2" ry="2.6" />
        <ellipse class="pet__eye" cx="25" cy="22" rx="2" ry="2.6" />
        <circle class="pet__cheek" cx="11.5" cy="26" r="2" />
        <circle class="pet__cheek" cx="28.5" cy="26" r="2" />
        <path class="pet__mouth" d="M17 27 Q20 30 23 27" />
      </svg>
    </div>
  </div>
</template>

<script setup lang="ts">
import { onMounted, onUnmounted, ref } from "vue";

type Poop = { id: number; x: number; fading: boolean };
type Food = { x: number; eaten: boolean };

// Position is a percentage across the strip (5..95 keeps the sprite fully on screen).
const x = ref(50);
const facing = ref<1 | -1>(1);
const walking = ref(false);
const eating = ref(false);
const poops = ref<Poop[]>([]);
const food = ref<Food | null>(null);

const MIN_X = 6;
const MAX_X = 94;
const STEP = 0.55; // % per tick while walking
let target = 50;
let poopSeq = 0;

const timers: ReturnType<typeof setTimeout>[] = [];
let raf = 0;
let stopped = false;

const rand = (lo: number, hi: number) => lo + Math.random() * (hi - lo);
const later = (fn: () => void, ms: number) => {
  const t = setTimeout(fn, ms);
  timers.push(t);
  return t;
};

// Walk toward `target`; when reached, settle and (if walking to food) eat.
function tick() {
  if (stopped) return;
  if (food.value && !food.value.eaten) {
    target = food.value.x; // always head for the snack
  }
  const dx = target - x.value;
  if (Math.abs(dx) > STEP) {
    walking.value = true;
    facing.value = dx > 0 ? 1 : -1;
    x.value = Math.max(MIN_X, Math.min(MAX_X, x.value + Math.sign(dx) * STEP));
  } else {
    x.value = target;
    if (walking.value) walking.value = false;
    if (food.value && !food.value.eaten && Math.abs(dx) <= STEP) eat();
  }
  raf = requestAnimationFrame(tick);
}

// Pick a new wander destination, unless busy chasing a snack.
function roam() {
  if (stopped) return;
  if (!food.value) target = rand(MIN_X, MAX_X);
  later(roam, rand(2600, 5200));
}

function dropPoop() {
  if (stopped) return;
  const p: Poop = { id: ++poopSeq, x: x.value, fading: false };
  poops.value = [...poops.value, p];
  // linger, then fade and remove
  later(() => {
    p.fading = true;
    poops.value = [...poops.value];
    later(() => {
      poops.value = poops.value.filter((q) => q.id !== p.id);
    }, 1200);
  }, rand(14000, 22000));
  later(dropPoop, rand(16000, 30000));
}

function spawnFood() {
  if (stopped) return;
  if (!food.value) food.value = { x: rand(MIN_X, MAX_X), eaten: false };
  later(spawnFood, rand(18000, 34000));
}

function eat() {
  if (!food.value) return;
  eating.value = true;
  food.value.eaten = true;
  later(() => {
    eating.value = false;
    food.value = null;
  }, 900);
}

onMounted(() => {
  raf = requestAnimationFrame(tick);
  later(roam, rand(1500, 3000));
  later(dropPoop, rand(8000, 16000));
  later(spawnFood, rand(10000, 20000));
});

onUnmounted(() => {
  stopped = true;
  cancelAnimationFrame(raf);
  timers.forEach(clearTimeout);
});
</script>

<style scoped>
.pet {
  position: absolute;
  left: 0;
  right: 0;
  bottom: 0;
  height: 42px;
  pointer-events: none;
  overflow: hidden;
  background: linear-gradient(180deg, rgba(0, 0, 0, 0) 0%, rgba(0, 0, 0, 0.18) 70%);
  border-top: 1px dashed var(--iv-border, #232838);
}

.pet__sprite {
  position: absolute;
  bottom: 4px;
  width: 34px;
  height: 34px;
  transition: transform 0.2s ease;
  animation: pet-bob 2.4s ease-in-out infinite;
}
.pet__sprite--walking {
  animation: pet-bob 0.5s ease-in-out infinite;
}

.pet__shadow { fill: #000; opacity: 0.28; }
.pet__body { fill: var(--pet-body, #7aa2f7); }
.pet__sheen { fill: var(--pet-body-dark, #5a7fd6); opacity: 0.6; }
.pet__eye {
  fill: #14161f;
  transform-origin: center;
  animation: pet-blink 4.2s steps(1) infinite;
}
.pet__cheek { fill: #f7a8c0; opacity: 0.7; }
.pet__mouth {
  stroke: #14161f;
  stroke-width: 1.4;
  fill: none;
  stroke-linecap: round;
}
.pet__sprite--eating .pet__mouth {
  animation: pet-chew 0.3s ease-in-out 3;
}

.pet__poop {
  position: absolute;
  bottom: 2px;
  font-size: 12px;
  transform: translateX(-50%);
  transition: opacity 1s ease;
}
.pet__poop--fading { opacity: 0; }

.pet__food {
  position: absolute;
  bottom: 3px;
  font-size: 13px;
  transform: translateX(-50%);
  transition: opacity 0.4s ease, transform 0.4s ease;
}
.pet__food--eaten { opacity: 0; transform: translateX(-50%) scale(0.3); }

@keyframes pet-bob {
  0%, 100% { transform: translateY(0); }
  50% { transform: translateY(-3px); }
}
@keyframes pet-blink {
  0%, 92%, 100% { transform: scaleY(1); }
  95% { transform: scaleY(0.1); }
}
@keyframes pet-chew {
  0%, 100% { transform: translateY(0); }
  50% { transform: translateY(1.5px); }
}

@media (prefers-reduced-motion: reduce) {
  .pet__sprite, .pet__eye { animation: none; }
}
</style>
