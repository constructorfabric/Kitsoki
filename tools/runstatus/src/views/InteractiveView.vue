<template>
  <div class="iv">
    <div v-if="store.loading" class="iv__loading">Loading session…</div>
    <template v-else>
      <!-- Top bar -->
      <header class="iv__topbar">
        <router-link to="/" class="iv__back" data-testid="back-stories">← Stories</router-link>
        <span class="iv__app-id">{{ appId }}</span>
        <span class="iv__sep">·</span>
        <code class="iv__current-state" data-testid="current-state">{{ store.currentStatePath || "—" }}</code>
        <span
          class="iv__state-badge"
          data-testid="state-badge"
          :data-terminal="store.terminal ? 'true' : 'false'"
          :class="store.terminal ? 'iv__state-badge--done' : 'iv__state-badge--live'"
        >
          {{ store.terminal ? 'done' : 'live' }}
        </span>
        <!-- Running agent spend for this session. Always shown (not gated on
             `present`) so the operator can see the live cost ticking — which in a
             deterministic run is exactly the point: every state transition, guard,
             and git host call is free, so this reads $0.0000 until (and unless) an
             agent touchpoint fires. The :class flags the zero state for emphasis. -->
        <span
          class="iv__usage"
          :class="{ 'iv__usage--zero': !store.usageTotals.present }"
          data-testid="usage-meter"
          :title="`${store.usageTotals.calls} agent call(s) · in ${fmtTokens(store.usageTotals.promptTokens)} / out ${fmtTokens(store.usageTotals.responseTokens)} tokens — deterministic transitions and git calls are free`"
        >
          Σ {{ fmtTokens(store.usageTotals.promptTokens + store.usageTotals.responseTokens) }} tok · {{ fmtCost(store.usageTotals.costUsd) }}
        </span>
        <span
          v-if="store.harnessProfiles.length"
          class="iv__harness"
          data-testid="harness-picker"
        >
          <select
            class="iv__harness-select"
            data-testid="provider-select"
            title="Harness profile (backend/provider) — takes effect next turn"
            :value="store.harnessActiveProfile"
            @change="onProviderChange"
          >
            <option v-for="p in store.harnessProfiles" :key="p.name" :value="p.name">{{ p.name }}</option>
          </select>
          <select
            v-if="activeModels.length"
            class="iv__harness-select"
            data-testid="model-select"
            title="Model for the active profile — takes effect next turn"
            :value="activeModel"
            @change="onModelChange"
          >
            <option v-for="m in activeModels" :key="m" :value="m">{{ shortModel(m) }}</option>
          </select>
          <select
            v-if="activeEfforts.length"
            class="iv__harness-select"
            data-testid="effort-select"
            title="Reasoning effort — where the model supports it; takes effect next turn"
            :value="activeEffort"
            @change="onEffortChange"
          >
            <option v-for="e in activeEfforts" :key="e" :value="e">effort: {{ e }}</option>
          </select>
        </span>
        <ProposalsBadge />
        <StoryFreshness
          :session-id="sessionId"
          :on-reloaded="onFreshnessReloaded"
          :on-reload-error="onFreshnessError"
          data-testid="story-freshness-widget"
        />
        <button
          v-if="!embed"
          type="button"
          class="iv__trace-toggle"
          data-testid="trace-column-toggle"
          :aria-expanded="!traceCollapsed"
          :title="traceCollapsed ? 'Show trace column' : 'Hide trace column'"
          @click="toggleTraceColumn"
        >
          {{ traceCollapsed ? 'Show trace' : 'Hide trace' }}
        </button>
        <button
          v-if="!embed && mediaItems.length > 0"
          type="button"
          class="iv__workbench-toggle"
          data-testid="media-workbench-toggle"
          :aria-pressed="workbenchEnabled"
          :title="workbenchEnabled ? 'Return media to the chat transcript' : 'Pin media beside the chat'"
          @click="toggleWorkbench"
        >
          {{ workbenchEnabled ? 'Close media pane' : 'Open media pane' }}
        </button>
        <MetaButton v-if="embed" placement="topbar" />
      </header>

      <!-- Reconnecting banner: the live trace stream dropped and the transport
           is backing off + reopening. Without this a stalled stream looks
           identical to a slow agent — dead air. -->
      <div
        v-if="store.connectionState === 'reconnecting'"
        class="iv__reconnecting"
        data-testid="reconnecting-banner"
        role="status"
      >
        <span class="iv__reconnecting-dot" aria-hidden="true"></span>
        Reconnecting to session…
      </div>

      <!-- Reload warning: shown when the current state was removed by the edit. -->
      <div
        v-if="reloadWarning"
        class="iv__reload-warning"
        data-testid="reload-warning"
      >
        {{ reloadWarning }}
      </div>

      <div
        v-if="operationRun"
        class="iv__operation"
        :class="operationRunClass"
        data-testid="operation-run-banner"
        role="status"
        :data-operation-status="operationRun.status"
      >
        <span class="iv__operation-dot" aria-hidden="true"></span>
        <span class="iv__operation-label">Operation</span>
        <strong class="iv__operation-title" data-testid="operation-run-title">
          {{ operationRun.title }}
        </strong>
        <span class="iv__operation-status" data-testid="operation-run-status">
          {{ operationRunStatusLabel }}
        </span>
        <span v-if="operationRunRoute" class="iv__operation-route">
          {{ operationRunRoute }}
        </span>
        <span v-if="operationRunDetail" class="iv__operation-detail" data-testid="operation-run-detail">
          {{ operationRunDetail }}
        </span>
        <span v-if="operationRunArtifact" class="iv__operation-artifact" data-testid="operation-run-artifact">
          {{ operationRunArtifact }}
        </span>
        <span
          v-if="operationRunArtifactHref || canDriveOperation"
          class="iv__operation-actions"
        >
          <a
            v-if="operationRunArtifactHref"
            class="iv__operation-action"
            data-testid="operation-run-artifact-open"
            :href="operationRunArtifactHref"
            target="_blank"
            rel="noopener noreferrer"
            :title="`Open ${operationRunArtifactHandle}`"
          >Open</a>
          <button
            v-if="canDriveOperation"
            type="button"
            class="iv__operation-action"
            data-testid="operation-run-drive"
            :disabled="pending || store.busy || drivingOperation"
            :title="drivingOperation ? 'Driving operation' : 'Drive operation to the next checkpoint'"
            @click="onDriveOperation"
          >
            {{ drivingOperation ? 'Driving' : 'Drive' }}
          </button>
        </span>
        <span
          v-if="operationRunFacts.length > 0"
          class="iv__operation-facts"
          data-testid="operation-run-summary"
        >
          <span
            v-for="fact in operationRunFacts"
            :key="fact.label"
            class="iv__operation-fact"
          >
            <span class="iv__operation-fact-label">{{ fact.label }}</span>
            {{ fact.value }}
          </span>
        </span>
      </div>

      <!-- Main row: chat (left) | trace (right).
           Browser: chat | resizable/collapsible diagram+timeline trace column.
           Embed (VS Code): chat ONLY — trace + graph live in their own dockable
           windows (the "Kitsoki Surfaces" panels), so the chat panel never repeats
           them. -->
      <div
        v-if="workbenchEnabled && !embed"
        class="iv__workbench-bar"
        data-testid="media-workbench-bar"
      >
        <label class="iv__workbench-field">
          <span>Media</span>
          <select
            v-model="selectedMediaKey"
            class="iv__workbench-select"
            data-testid="media-workbench-select"
          >
            <option v-for="item in mediaItems" :key="item.key" :value="item.key">
              {{ item.title }}
            </option>
          </select>
        </label>
        <span class="iv__segmented" role="group" aria-label="Workbench orientation">
          <button
            type="button"
            class="iv__segmented-btn"
            data-testid="workbench-orient-vertical"
            :aria-pressed="workbenchOrientation === 'vertical'"
            @click="workbenchOrientation = 'vertical'"
          >Vertical</button>
          <button
            type="button"
            class="iv__segmented-btn"
            data-testid="workbench-orient-horizontal"
            :aria-pressed="workbenchOrientation === 'horizontal'"
            @click="workbenchOrientation = 'horizontal'"
          >Horizontal</button>
        </span>
        <span class="iv__segmented" role="group" aria-label="Devtools dock">
          <button
            type="button"
            class="iv__segmented-btn"
            data-testid="devtools-dock-right"
            :aria-pressed="devtoolsDock === 'right'"
            @click="devtoolsDock = 'right'"
          >Dock right</button>
          <button
            type="button"
            class="iv__segmented-btn"
            data-testid="devtools-dock-bottom"
            :aria-pressed="devtoolsDock === 'bottom'"
            @click="devtoolsDock = 'bottom'"
          >Dock bottom</button>
          <button
            type="button"
            class="iv__segmented-btn"
            data-testid="devtools-dock-float"
            :aria-pressed="devtoolsDock === 'floating'"
            @click="devtoolsDock = 'floating'"
          >Float</button>
        </span>
        <button
          type="button"
          class="iv__workbench-action"
          data-testid="devtools-popout"
          title="Open the selected devtools surface in a separate browser window"
          @click="popOutDevtools"
        >Pop out</button>
      </div>

      <div
        class="iv__main"
        :class="{
          'iv__main--embed': embed,
          'iv__main--trace-collapsed': traceCollapsed,
          'iv__main--workbench': workbenchEnabled && !embed,
          'iv__main--workbench-vertical': workbenchEnabled && !embed && workbenchOrientation === 'vertical',
          'iv__main--workbench-horizontal': workbenchDevtoolsVisible && workbenchOrientation === 'horizontal',
          'iv__main--devtools-bottom': workbenchDevtoolsVisible && devtoolsDock === 'bottom',
          'iv__main--devtools-floating': workbenchDevtoolsVisible && devtoolsDock === 'floating',
        }"
        :style="workbenchMainStyle"
      >
        <section
          v-if="workbenchEnabled && !embed"
          class="iv__media-pane"
          aria-label="Pinned media"
          data-testid="media-workbench-pane"
        >
          <div class="iv__pane-header">
            <span>{{ selectedMedia?.title || 'Media' }}</span>
            <span v-if="store.embedLabel" class="iv__pane-subtitle">{{ store.embedLabel }}</span>
          </div>
          <div class="iv__media-stage" data-testid="media-workbench-stage">
            <ViewElement
              v-if="selectedMedia?.element"
              :element="selectedMedia.element"
              :show-pin="false"
            />
            <div v-else class="iv__empty">No media selected.</div>
          </div>
        </section>
        <button
          v-if="workbenchEnabled && !embed"
          type="button"
          class="iv__resize-handle iv__resize-handle--column iv__resize-handle--workbench-media"
          data-testid="media-workbench-resizer"
          role="separator"
          aria-label="Resize pinned media and chat panes"
          aria-orientation="vertical"
          :aria-valuenow="Math.round(workbenchMediaWidthPercent)"
          aria-valuemin="24"
          aria-valuemax="68"
          @pointerdown="startWorkbenchMediaResize"
          @keydown="onWorkbenchMediaResizeKeydown"
        ></button>

        <!-- LEFT: conversation -->
        <section
          class="iv__chat"
          aria-label="Conversation"
          data-testid="chat-section"
          :style="chatColumnStyle"
        >
          <div
            v-if="workbenchEnabled && selectedMedia"
            class="iv__chat-media-context"
            data-testid="chat-pinned-context"
          >
            <span class="iv__chat-media-context-label">Working on</span>
            <strong :title="selectedMedia.title">{{ selectedMedia.title }}</strong>
            <span class="iv__chat-media-context-hint">Pinned in the workbench</span>
          </div>
          <div
            v-if="focusedChat || focusedChatLoading || focusedChatError"
            class="iv__focused-chat"
            data-testid="focused-chat"
          >
            <div class="iv__focused-chat-head">
              <span class="iv__focused-chat-label">Subagent</span>
              <strong>{{ focusedChat?.chat.title || focusedChatID || "Loading chat" }}</strong>
              <button
                type="button"
                class="iv__focused-chat-close"
                data-testid="focused-chat-close"
                title="Close focused chat context"
                @click="clearFocusedChat"
              >close</button>
            </div>
            <div v-if="focusedChatLoading" class="iv__focused-chat-muted">Loading focused context...</div>
            <div v-else-if="focusedChatError" class="iv__focused-chat-error">{{ focusedChatError }}</div>
            <template v-else-if="focusedChat">
              <div class="iv__focused-chat-meta">
                <span v-if="focusedChat.context?.session_id">session {{ focusedChat.context.session_id }}</span>
                <span v-if="focusedChatScope">scope {{ focusedChatScope }}</span>
                <span>chat {{ focusedChat.chat.id }}</span>
                <span>{{ focusedChat.chat.status }}</span>
                <span v-if="focusedChat.pty">tmux {{ focusedChat.pty.tmux_session }}</span>
                <span v-if="focusedChat.pty?.mode">{{ focusedChat.pty.mode }}</span>
              </div>
              <div v-if="focusedChat.messages?.length" class="iv__focused-chat-messages">
                <div
                  v-for="m in focusedChatPreview"
                  :key="m.seq"
                  class="iv__focused-chat-message"
                >
                  <span class="iv__focused-chat-role">{{ m.role }}</span>
                  <span class="iv__focused-chat-content">{{ m.content }}</span>
                </div>
              </div>
            </template>
          </div>
          <ChatTranscript
            class="iv__transcript"
            :transcript="store.chatEntries"
            :suppressed-media-handles="suppressedMediaHandles"
            :suppressed-media-labels="suppressedMediaLabels"
            @rewind="onRewind"
            @feedback="onFeedback"
          />
          <!-- Streaming thinking bubble: visible while a turn is in flight -->
          <div v-if="pending || store.busy" class="iv__thinking" data-testid="thinking-bubble">
            <div class="iv__thinking-avatar">A</div>
            <div class="iv__thinking-bubble">
              <div class="iv__thinking-head">
              <div class="iv__thinking-role">Agent</div>
              <!-- Stop: cancels the agent server-side, not just the frontend. -->
              <button
                type="button"
                class="iv__thinking-stop"
                data-testid="cancel-agent"
                :disabled="cancelling"
                @click="onCancel"
              >
                {{ cancelling ? "Stopping…" : "■ Stop" }}
              </button>
            </div>
              <!-- The live feed, in arrival order: thinking prose (🧠, like the
                   TUI) interleaved with the tool calls it explains — the same
                   shared ActivityFeed the preserved disclosure and the meta
                   overlay render, so the stream looks identical everywhere. -->
              <ActivityFeed :items="store.pendingStream" />
              <!-- Trailing dots while the turn is in flight: the bubble only
                   exists mid-turn, so "more is coming" is always true here. -->
              <div class="iv__thinking-dots"><span>·</span><span>·</span><span>·</span></div>
            </div>
          </div>
          <div v-if="store.terminal" class="iv__done-note">
            Session complete — no further input accepted.
          </div>
          <ImprovePrompt
            v-if="store.terminal"
            :session-id="props.sessionId"
          />
          <InputBar
            v-else
            :intents="store.currentView?.intents ?? []"
            :typed-view="store.currentView?.typed_view"
            :default-intent="store.currentView?.default_intent"
            :pending="pending"
            :initial-raw-draft="initialRawDraft"
            @send="onSend"
            @intent="onIntent"
          />
          <div v-if="error" class="iv__error">{{ error }}</div>
        </section>

        <!-- RIGHT (browser): live trace (diagram over timeline) -->
        <button
          v-if="!embed && !traceCollapsed && !workbenchEnabled"
          type="button"
          class="iv__resize-handle iv__resize-handle--column"
          data-testid="trace-column-resizer"
          role="separator"
          aria-label="Resize chat and trace columns"
          aria-orientation="vertical"
          :aria-valuenow="Math.round(traceWidthPercent)"
          aria-valuemin="24"
          aria-valuemax="75"
          @pointerdown="startColumnResize"
          @keydown="onColumnResizeKeydown"
        ></button>
        <section
          v-if="!embed && !traceCollapsed && !workbenchEnabled"
          class="iv__trace"
          aria-label="Trace"
          :style="traceColumnStyle"
        >
          <div
            class="iv__panel iv__panel--diagram"
            data-testid="trace-diagram"
            :style="diagramPanelStyle"
          >
            <div class="iv__panel-header">
              <span>State Diagram</span>
              <span class="iv__panel-actions">
                <button
                  type="button"
                  class="iv__panel-action iv__panel-action--pet"
                  data-testid="trace-pet-toggle"
                  :aria-pressed="petEnabled"
                  :title="petEnabled ? 'Hide trace pet' : 'Show trace pet'"
                  @click="toggleTracePet"
                >🐾</button>
                <button
                  type="button"
                  class="iv__panel-action"
                  title="Hide trace column"
                  @click="toggleTraceColumn"
                >hide</button>
              </span>
            </div>
            <StateDiagram
              v-if="store.mermaid"
              :mermaid-source="store.mermaid.source"
              :node-map="store.mermaid.node_map"
              :current-state-path="store.currentStatePath"
              :highlighted-state-paths="store.highlightedStatePaths"
              :events="store.events"
              :selected-event-index="store.selectedEventIndex"
              :intents="store.currentView?.intents ?? []"
              @select="onNodeSelect"
              @select-phase="onPhaseSelect"
              @select-event="onEventSelect"
            />
            <div v-else class="iv__empty">No diagram.</div>
          </div>
          <button
            type="button"
            class="iv__resize-handle iv__resize-handle--row"
            data-testid="trace-row-resizer"
            role="separator"
            aria-label="Resize state diagram and trace rows"
            aria-orientation="horizontal"
            :aria-valuenow="Math.round(diagramHeightPercent)"
            aria-valuemin="18"
            aria-valuemax="82"
            @pointerdown="startRowResize"
            @keydown="onRowResizeKeydown"
          ></button>
          <div
            class="iv__panel iv__panel--timeline"
            data-testid="trace-timeline"
            :style="timelinePanelStyle"
          >
            <div class="iv__panel-header">
              <span>Trace</span>
              <button
                v-if="store.highlightedStatePaths.length > 0"
                class="iv__clear-highlight"
                title="Clear diagram highlight"
                @click="onClearHighlight"
              >clear highlight ({{ store.highlightedStatePaths.length }})</button>
            </div>
            <TraceTimeline
              :events="store.events"
              :selected-event-index="store.selectedEventIndex"
              :highlighted-state-paths="store.highlightedStatePaths"
              :highlight-tick="store.highlightTick"
              :mermaid-source="store.mermaid?.source ?? null"
              @select="onEventSelect"
            />
          </div>
          <TracePet v-if="petEnabled" />
        </section>

        <!-- Embed (VS Code): nothing beside the chat — Trace and Graph open as
             their own dockable windows via "Kitsoki: Open Trace" / "Open Graph". -->
        <button
          v-if="workbenchDevtoolsVisible && devtoolsDock === 'right' && workbenchOrientation === 'vertical'"
          type="button"
          class="iv__resize-handle iv__resize-handle--column iv__resize-handle--workbench-devtools"
          data-testid="devtools-workbench-resizer"
          role="separator"
          aria-label="Resize chat and devtools panes"
          aria-orientation="vertical"
          :aria-valuenow="Math.round(workbenchDevtoolsWidthPercent)"
          aria-valuemin="20"
          aria-valuemax="50"
          @pointerdown="startWorkbenchDevtoolsWidthResize"
          @keydown="onWorkbenchDevtoolsWidthResizeKeydown"
        ></button>
        <button
          v-if="workbenchDevtoolsVisible && (devtoolsDock === 'bottom' || workbenchOrientation === 'horizontal')"
          type="button"
          class="iv__resize-handle iv__resize-handle--row iv__resize-handle--workbench-devtools-row"
          data-testid="devtools-workbench-row-resizer"
          role="separator"
          aria-label="Resize main workbench and devtools rows"
          aria-orientation="horizontal"
          :aria-valuenow="Math.round(workbenchDevtoolsHeightPercent)"
          aria-valuemin="22"
          aria-valuemax="55"
          @pointerdown="startWorkbenchDevtoolsHeightResize"
          @keydown="onWorkbenchDevtoolsHeightResizeKeydown"
        ></button>
        <section
          v-if="workbenchDevtoolsVisible && devtoolsDock !== 'floating'"
          class="iv__devtools"
          aria-label="Devtools"
          data-testid="media-devtools-pane"
        >
          <div class="iv__pane-header">
            <span class="iv__devtools-tabs" role="tablist">
              <button
                type="button"
                class="iv__devtools-tab"
                data-testid="devtools-tab-graph"
                :aria-selected="devtoolsTab === 'graph'"
                @click="devtoolsTab = 'graph'"
              >Graph</button>
              <button
                type="button"
                class="iv__devtools-tab"
                data-testid="devtools-tab-trace"
                :aria-selected="devtoolsTab === 'trace'"
                @click="devtoolsTab = 'trace'"
              >Trace</button>
            </span>
            <button type="button" class="iv__pane-action" @click="toggleTraceColumn">hide</button>
          </div>
          <div class="iv__devtools-body">
            <StateDiagram
              v-if="devtoolsTab === 'graph' && store.mermaid"
              :mermaid-source="store.mermaid.source"
              :node-map="store.mermaid.node_map"
              :current-state-path="store.currentStatePath"
              :highlighted-state-paths="store.highlightedStatePaths"
              :events="store.events"
              :selected-event-index="store.selectedEventIndex"
              :intents="store.currentView?.intents ?? []"
              @select="onNodeSelect"
              @select-phase="onPhaseSelect"
              @select-event="onEventSelect"
            />
            <TraceTimeline
              v-else-if="devtoolsTab === 'trace'"
              :events="store.events"
              :selected-event-index="store.selectedEventIndex"
              :highlighted-state-paths="store.highlightedStatePaths"
              :highlight-tick="store.highlightTick"
              :mermaid-source="store.mermaid?.source ?? null"
              @select="onEventSelect"
            />
            <div v-else class="iv__empty">No diagram.</div>
          </div>
        </section>
      </div>
      <section
        v-if="workbenchDevtoolsVisible && devtoolsDock === 'floating'"
        class="iv__floating-devtools"
        aria-label="Floating devtools"
        data-testid="floating-devtools-pane"
      >
        <div class="iv__pane-header">
          <span class="iv__devtools-tabs" role="tablist">
            <button
              type="button"
              class="iv__devtools-tab"
              :aria-selected="devtoolsTab === 'graph'"
              @click="devtoolsTab = 'graph'"
            >Graph</button>
            <button
              type="button"
              class="iv__devtools-tab"
              :aria-selected="devtoolsTab === 'trace'"
              @click="devtoolsTab = 'trace'"
            >Trace</button>
          </span>
          <button
            type="button"
            class="iv__pane-action"
            data-testid="floating-devtools-dock"
            @click="devtoolsDock = 'right'"
          >dock</button>
        </div>
        <div class="iv__devtools-body">
          <StateDiagram
            v-if="devtoolsTab === 'graph' && store.mermaid"
            :mermaid-source="store.mermaid.source"
            :node-map="store.mermaid.node_map"
            :current-state-path="store.currentStatePath"
            :highlighted-state-paths="store.highlightedStatePaths"
            :events="store.events"
            :selected-event-index="store.selectedEventIndex"
            :intents="store.currentView?.intents ?? []"
            @select="onNodeSelect"
            @select-phase="onPhaseSelect"
            @select-event="onEventSelect"
          />
          <TraceTimeline
            v-else-if="devtoolsTab === 'trace'"
            :events="store.events"
            :selected-event-index="store.selectedEventIndex"
            :highlighted-state-paths="store.highlightedStatePaths"
            :highlight-tick="store.highlightTick"
            :mermaid-source="store.mermaid?.source ?? null"
            @select="onEventSelect"
          />
          <div v-else class="iv__empty">No diagram.</div>
        </div>
      </section>
    </template>
  </div>
</template>

<script setup lang="ts">
import { computed, onMounted, onUnmounted, ref, watch } from "vue";
import { useRoute, useRouter } from "vue-router";
import { useRunStore, type TranscriptEntry } from "../stores/run.js";
import { useInboxStore } from "../stores/inbox.js";
import { createDataSource } from "../data/source.js";
import type { DataSource } from "../data/source.js";
import { SnapshotSource } from "../data/snapshot-source.js";
import { LiveSource, TurnCancelledError } from "../data/live-source.js";
import type { ChatMessageItem, ChatShowResult } from "../data/live-source.js";
import { markAutoNavDone } from "../lib/auto-nav.js";
import ActivityFeed from "../components/ActivityFeed.vue";
import ChatTranscript from "../components/ChatTranscript.vue";
import InputBar from "../components/InputBar.vue";
import StateDiagram from "../components/StateDiagram.vue";
import TraceTimeline from "../components/TraceTimeline.vue";
import TracePet from "../components/TracePet.vue";
import ViewElement from "../components/ViewElement.vue";
import StoryFreshness from "../components/StoryFreshness.vue";
import MetaButton from "../components/meta/MetaButton.vue";
import ImprovePrompt from "../components/meta/ImprovePrompt.vue";
import ProposalsBadge from "../components/ProposalsBadge.vue";
import { useProposalsStore } from "../stores/proposals.js";
import type { Proposal } from "../stores/proposals.js";
import { fmtTokens, fmtCost } from "../components/agent/lib.js";
import { isEmbedded } from "../lib/embed.js";
import type { NodeRef, ViewElement as ViewElementT } from "../types.js";

const GITHUB_INBOX_REFRESH_INTERVAL_MS = 5 * 60 * 1000;
const TRACE_WIDTH_DEFAULT = 54;
const TRACE_WIDTH_MIN = 24;
const TRACE_WIDTH_MAX = 75;
const DIAGRAM_HEIGHT_DEFAULT = 45;
const DIAGRAM_HEIGHT_MIN = 18;
const DIAGRAM_HEIGHT_MAX = 82;
const WORKBENCH_PREF_KEY = "kitsoki:mediaWorkbench";
const WORKBENCH_MEDIA_WIDTH_DEFAULT = 42;
const WORKBENCH_MEDIA_WIDTH_MIN = 24;
const WORKBENCH_MEDIA_WIDTH_MAX = 68;
const WORKBENCH_DEVTOOLS_WIDTH_DEFAULT = 28;
const WORKBENCH_DEVTOOLS_WIDTH_MIN = 20;
const WORKBENCH_DEVTOOLS_WIDTH_MAX = 50;
const WORKBENCH_DEVTOOLS_HEIGHT_DEFAULT = 34;
const WORKBENCH_DEVTOOLS_HEIGHT_MIN = 22;
const WORKBENCH_DEVTOOLS_HEIGHT_MAX = 55;

type MediaWorkbenchItem = {
  key: string;
  handle: string;
  title: string;
  element: ViewElementT;
};
type WorkbenchPrefs = {
  orientation?: "vertical" | "horizontal";
  devtoolsDock?: "right" | "bottom" | "floating";
  devtoolsTab?: "graph" | "trace";
  mediaWidthPercent?: number;
  devtoolsWidthPercent?: number;
  devtoolsHeightPercent?: number;
};

function loadWorkbenchPrefs(): WorkbenchPrefs {
  try {
    const raw = localStorage.getItem(WORKBENCH_PREF_KEY);
    if (!raw) return {};
    const parsed = JSON.parse(raw) as WorkbenchPrefs;
    return {
      orientation: parsed.orientation === "horizontal" ? "horizontal" : parsed.orientation === "vertical" ? "vertical" : undefined,
      devtoolsDock:
        parsed.devtoolsDock === "bottom" || parsed.devtoolsDock === "floating" || parsed.devtoolsDock === "right"
          ? parsed.devtoolsDock
          : undefined,
      devtoolsTab: parsed.devtoolsTab === "trace" ? "trace" : parsed.devtoolsTab === "graph" ? "graph" : undefined,
      mediaWidthPercent: Number.isFinite(parsed.mediaWidthPercent) ? parsed.mediaWidthPercent : undefined,
      devtoolsWidthPercent: Number.isFinite(parsed.devtoolsWidthPercent) ? parsed.devtoolsWidthPercent : undefined,
      devtoolsHeightPercent: Number.isFinite(parsed.devtoolsHeightPercent) ? parsed.devtoolsHeightPercent : undefined,
    };
  } catch {
    return {};
  }
}

const props = defineProps<{ sessionId: string }>();
const store = useRunStore();
const route = useRoute();
const router = useRouter();
const inbox = useInboxStore();
const proposals = useProposalsStore();

// Embed layout (VS Code webview): chat front/center with a hint rail that
// chat is shown ALONE (trace + graph have their own dockable windows). The
// standalone browser app keeps its full layout (chat | diagram + timeline).
const embed = computed(() => isEmbedded() || route?.query?.embed === "1");
const initialRawDraft = ref(queryString(route?.query?.draft));

// One DataSource for the lifetime of the view (subscribe + write RPCs).
let source: DataSource | null = null;
let githubInboxTimer: ReturnType<typeof setInterval> | null = null;
let focusedChatSeq = 0;

// True while a turn is in flight; disables the input so the operator can't
// fire a second overlapping turn against the live session.
const pending = ref(false);
// True between clicking Stop and the turn actually aborting — keeps the button
// from firing a second cancel and gives the operator immediate feedback.
const cancelling = ref(false);
const drivingOperation = ref(false);
const error = ref<string | null>(null);
const sourceCanDriveOperation = ref(false);
const traceCollapsed = ref(false);
const traceWidthPercent = ref(TRACE_WIDTH_DEFAULT);
const diagramHeightPercent = ref(DIAGRAM_HEIGHT_DEFAULT);
const workbenchEnabled = ref(false);
const selectedMediaKey = ref("");
const savedWorkbenchPrefs = loadWorkbenchPrefs();
const workbenchOrientation = ref<"vertical" | "horizontal">(savedWorkbenchPrefs.orientation ?? "vertical");
const devtoolsDock = ref<"right" | "bottom" | "floating">(savedWorkbenchPrefs.devtoolsDock ?? "right");
const devtoolsTab = ref<"graph" | "trace">(savedWorkbenchPrefs.devtoolsTab ?? "graph");
const workbenchMediaWidthPercent = ref(
  clamp(savedWorkbenchPrefs.mediaWidthPercent ?? WORKBENCH_MEDIA_WIDTH_DEFAULT, WORKBENCH_MEDIA_WIDTH_MIN, WORKBENCH_MEDIA_WIDTH_MAX),
);
const workbenchDevtoolsWidthPercent = ref(
  clamp(savedWorkbenchPrefs.devtoolsWidthPercent ?? WORKBENCH_DEVTOOLS_WIDTH_DEFAULT, WORKBENCH_DEVTOOLS_WIDTH_MIN, WORKBENCH_DEVTOOLS_WIDTH_MAX),
);
const workbenchDevtoolsHeightPercent = ref(
  clamp(savedWorkbenchPrefs.devtoolsHeightPercent ?? WORKBENCH_DEVTOOLS_HEIGHT_DEFAULT, WORKBENCH_DEVTOOLS_HEIGHT_MIN, WORKBENCH_DEVTOOLS_HEIGHT_MAX),
);
const workbenchDevtoolsVisible = computed(
  () => workbenchEnabled.value && !embed.value && !traceCollapsed.value,
);

// Opt-in decorative trace-column pet (off by default). Persisted in localStorage
// so the choice sticks across reloads. See TracePet.vue.
const PET_PREF_KEY = "kitsoki:tracePet";
const petEnabled = ref(false);
try {
  petEnabled.value = localStorage.getItem(PET_PREF_KEY) === "1";
} catch {
  /* localStorage unavailable (private mode / sandbox) — leave off */
}
function toggleTracePet() {
  petEnabled.value = !petEnabled.value;
  try {
    localStorage.setItem(PET_PREF_KEY, petEnabled.value ? "1" : "0");
  } catch {
    /* ignore persistence failure */
  }
}

const appId = computed(() => store.appDef?.id ?? store.appDef?.name ?? "kitsoki");
const operationRun = computed(() => store.operationRun);
type OperationFact = { label: string; value: string };
const operationRunClass = computed(() => ({
  "iv__operation--completed": operationRun.value?.status === "completed",
  "iv__operation--failed": operationRun.value?.status === "failed",
  "iv__operation--waiting": operationRun.value?.status === "waiting",
}));
const operationRunStatusLabel = computed(() => {
  const run = operationRun.value;
  if (!run) return "";
  const status = run.status || "running";
  if (status === "waiting" && run.stopReason) return `waiting for ${run.stopReason}`;
  if (run.runInBackground && status === "running") return "running in background";
  return status.replace(/_/g, " ");
});
const operationRunRoute = computed(() => {
  const run = operationRun.value;
  if (!run) return "";
  if (run.status === "waiting" && run.terminalState) {
    return `parked at ${run.terminalState}`;
  }
  if (run.status === "completed" && run.terminalState) {
    return `terminal ${run.terminalState}`;
  }
  if (run.phase) return `phase ${operationPhaseLabel(run.phase)}`;
  if (run.from && run.to) return `${run.from} -> ${run.to}`;
  return run.entryIntent ? `intent ${run.entryIntent}` : "";
});
const canDriveOperation = computed(() => {
  const run = operationRun.value;
  if (!run || store.terminal || !sourceCanDriveOperation.value) return false;
  return (run.status || "running") === "running" && operationModeCanDrive(run.mode);
});
function operationModeCanDrive(mode?: string): boolean {
  return !mode || mode === "autonomous" || mode === "supervised";
}
const operationRunDetail = computed(() => {
  const detail = operationRun.value?.stopDetail;
  return detail ? `needs input: ${detail}` : "";
});
const operationRunArtifactLabel = computed(() => operationRun.value?.terminalArtifact ?? "");
const operationRunArtifactHandle = computed(
  () => operationRun.value?.terminalArtifactHandle ?? operationRun.value?.terminalArtifact ?? ""
);
const operationRunArtifact = computed(() => {
  const artifact = operationRunArtifactLabel.value;
  return artifact ? `artifact ${artifact}` : "";
});
const operationRunArtifactHref = computed(() => {
  const artifact = operationRunArtifactHandle.value;
  if (!artifact || !source) return "";
  return source.artifactUrl(artifact);
});
const operationRunFacts = computed<OperationFact[]>(() => {
  const run = operationRun.value;
  if (!run) return [];
  const facts: OperationFact[] = [];
  const add = (label: string, value?: string) => {
    if (value && value.trim()) facts.push({ label, value });
  };
  add("mode", run.mode);
  add("execution", run.executionMode);
  if (run.phase) add("phase", operationPhaseLabel(run.phase));
  if (run.from && run.to) add("route", `${run.from} -> ${run.to}`);
  add("intent", run.entryIntent);
  add("terminal", run.terminalState);
  add("artifact", run.terminalArtifact);
  add("stop", run.stopReason);
  return facts;
});
const chatColumnStyle = computed(() => {
  if (embed.value || traceCollapsed.value) return {};
  return { flex: `1 1 ${100 - traceWidthPercent.value}%` };
});
const traceColumnStyle = computed(() => ({
  flex: `0 0 ${traceWidthPercent.value}%`,
}));
const diagramPanelStyle = computed(() => ({
  flex: `0 0 ${diagramHeightPercent.value}%`,
}));
const timelinePanelStyle = computed(() => ({
  flex: `1 1 ${100 - diagramHeightPercent.value}%`,
}));
const workbenchMainStyle = computed((): Record<string, string> => {
  if (!workbenchEnabled.value || embed.value) return {};
  const mediaTrack = `minmax(18rem, ${workbenchMediaWidthPercent.value}%)`;
  const chatTrack = "minmax(20rem, 1fr)";
  const mainRow = "minmax(0, 1fr)";
  if (!workbenchDevtoolsVisible.value || devtoolsDock.value === "floating") {
    return {
      gridTemplateColumns: `${mediaTrack} 0.55rem ${chatTrack}`,
      gridTemplateRows: mainRow,
    };
  }
  if (devtoolsDock.value === "bottom" || workbenchOrientation.value === "horizontal") {
    return {
      gridTemplateColumns: `${mediaTrack} 0.55rem ${chatTrack}`,
      gridTemplateRows: `${mainRow} 0.55rem minmax(12rem, ${workbenchDevtoolsHeightPercent.value}%)`,
    };
  }
  return {
    gridTemplateColumns: `${mediaTrack} 0.55rem ${chatTrack} 0.55rem minmax(16rem, ${workbenchDevtoolsWidthPercent.value}%)`,
    gridTemplateRows: mainRow,
  };
});

watch(
  [
    workbenchOrientation,
    devtoolsDock,
    devtoolsTab,
    workbenchMediaWidthPercent,
    workbenchDevtoolsWidthPercent,
    workbenchDevtoolsHeightPercent,
  ],
  () => {
    try {
      localStorage.setItem(
        WORKBENCH_PREF_KEY,
        JSON.stringify({
          orientation: workbenchOrientation.value,
          devtoolsDock: devtoolsDock.value,
          devtoolsTab: devtoolsTab.value,
          mediaWidthPercent: workbenchMediaWidthPercent.value,
          devtoolsWidthPercent: workbenchDevtoolsWidthPercent.value,
          devtoolsHeightPercent: workbenchDevtoolsHeightPercent.value,
        } satisfies WorkbenchPrefs),
      );
    } catch {
      /* ignore persistence failure */
    }
  },
);

function mediaHandle(el: ViewElementT): string {
  return el.Handle ?? el.MediaHandle ?? "";
}

function mediaTitle(el: ViewElementT, index: number): string {
  return el.Caption ?? el.MediaCaption ?? mediaHandle(el) ?? `Media ${index + 1}`;
}

const mediaItems = computed<MediaWorkbenchItem[]>(() => {
  const items: MediaWorkbenchItem[] = [];
  for (const entry of store.chatEntries) {
    for (const el of entry.typedView?.Elements ?? []) {
      if (el.Kind !== "media") continue;
      const handle = mediaHandle(el);
      if (!handle) continue;
      items.push({
        key: `${handle}:${items.length}`,
        handle,
        title: mediaTitle(el, items.length),
        element: el,
      });
    }
  }
  return items;
});

const selectedMedia = computed<MediaWorkbenchItem | null>(() => {
  return mediaItems.value.find((item) => item.key === selectedMediaKey.value) ?? mediaItems.value.at(-1) ?? null;
});

const suppressedMediaHandles = computed<string[]>(() => {
  return workbenchEnabled.value && selectedMedia.value ? [selectedMedia.value.handle] : [];
});
const suppressedMediaLabels = computed<Record<string, string>>(() => {
  return workbenchEnabled.value && selectedMedia.value
    ? { [selectedMedia.value.handle]: selectedMedia.value.title }
    : {};
});

function selectLatestMedia(): void {
  const latest = mediaItems.value.at(-1);
  selectedMediaKey.value = latest?.key ?? "";
}

function toggleWorkbench(): void {
  if (!workbenchEnabled.value) selectLatestMedia();
  workbenchEnabled.value = !workbenchEnabled.value;
}

function onPinMedia(ev: Event): void {
  const detail = (ev as CustomEvent<{ handle?: string }>).detail;
  const handle = detail?.handle;
  const match = handle
    ? [...mediaItems.value].reverse().find((item) => item.handle === handle)
    : mediaItems.value.at(-1);
  selectedMediaKey.value = match?.key ?? "";
  workbenchEnabled.value = !!match;
}

function popOutDevtools(): void {
  const surface = devtoolsTab.value === "graph" ? "graph" : "trace";
  const url = `${location.origin}${location.pathname}?surface=${surface}`;
  window.open(url, `kitsoki-${surface}-${props.sessionId}`, "popup,width=760,height=760");
}

// ── Harness picker (mirrors RunView) ─────────────────────────────────────────
const activeProfileObj = computed(() => store.harnessProfiles.find((p) => p.active));
const activeModels = computed<string[]>(() => activeProfileObj.value?.models ?? []);
const activeModel = computed<string>(() => store.harnessModel || activeProfileObj.value?.model || "");
const activeEfforts = computed<string[]>(() => activeProfileObj.value?.efforts ?? []);
const activeEffort = computed<string>(() => store.harnessEffort || activeProfileObj.value?.effort || "");

async function onProviderChange(e: Event): Promise<void> {
  if (!source) source = createDataSource();
  await store.selectProfile(source, props.sessionId, (e.target as HTMLSelectElement).value);
}
async function onModelChange(e: Event): Promise<void> {
  if (!source) source = createDataSource();
  await store.selectProfile(source, props.sessionId, store.harnessActiveProfile, (e.target as HTMLSelectElement).value, store.harnessEffort);
}
async function onEffortChange(e: Event): Promise<void> {
  if (!source) source = createDataSource();
  await store.selectProfile(source, props.sessionId, store.harnessActiveProfile, store.harnessModel, (e.target as HTMLSelectElement).value);
}
function shortModel(m: string): string {
  const slash = m.lastIndexOf("/");
  return slash >= 0 ? m.slice(slash + 1) : m;
}

const reloadWarning = ref<string | null>(null);
const focusedChat = ref<ChatShowResult | null>(null);
const focusedChatLoading = ref(false);
const focusedChatError = ref<string | null>(null);
const focusedChatID = ref("");
const focusedChatPreview = computed<ChatMessageItem[]>(() =>
  (focusedChat.value?.messages ?? []).slice(-3)
);
const focusedChatScope = computed(() => {
  const chat = focusedChat.value?.chat;
  if (!chat) return "";
  return chat.display_scope_key || chat.scope_key || "";
});

function operationPhaseLabel(phase: string): string {
  return phase.trim().replace(/_artifact$/i, "").replace(/_/g, " ");
}

function canListWork(candidate: DataSource | null): candidate is DataSource & Pick<LiveSource, "listWork"> {
  return typeof (candidate as Partial<Pick<LiveSource, "listWork">> | null)?.listWork === "function";
}

function canSyncGitHubInbox(
  candidate: DataSource | null,
): candidate is DataSource & Pick<LiveSource, "syncGitHubInbox" | "listWork"> {
  const partial = candidate as Partial<Pick<LiveSource, "syncGitHubInbox" | "listWork">> | null;
  return typeof partial?.syncGitHubInbox === "function" && typeof partial.listWork === "function";
}

function clamp(value: number, min: number, max: number): number {
  return Math.min(max, Math.max(min, value));
}

function toggleTraceColumn(): void {
  traceCollapsed.value = !traceCollapsed.value;
}

function resizeColumnFromClientX(clientX: number): void {
  const main = document.querySelector<HTMLElement>(".iv__main");
  if (!main) return;
  const rect = main.getBoundingClientRect();
  if (rect.width <= 0) return;
  const next = ((rect.right - clientX) / rect.width) * 100;
  traceWidthPercent.value = clamp(next, TRACE_WIDTH_MIN, TRACE_WIDTH_MAX);
}

function resizeRowFromClientY(clientY: number): void {
  const trace = document.querySelector<HTMLElement>(".iv__trace");
  if (!trace) return;
  const rect = trace.getBoundingClientRect();
  if (rect.height <= 0) return;
  const next = ((clientY - rect.top) / rect.height) * 100;
  diagramHeightPercent.value = clamp(next, DIAGRAM_HEIGHT_MIN, DIAGRAM_HEIGHT_MAX);
}

function resizeWorkbenchMediaFromClientX(clientX: number): void {
  const main = document.querySelector<HTMLElement>(".iv__main--workbench");
  if (!main) return;
  const rect = main.getBoundingClientRect();
  if (rect.width <= 0) return;
  const next = ((clientX - rect.left) / rect.width) * 100;
  workbenchMediaWidthPercent.value = clamp(next, WORKBENCH_MEDIA_WIDTH_MIN, WORKBENCH_MEDIA_WIDTH_MAX);
}

function resizeWorkbenchDevtoolsWidthFromClientX(clientX: number): void {
  const main = document.querySelector<HTMLElement>(".iv__main--workbench");
  if (!main) return;
  const rect = main.getBoundingClientRect();
  if (rect.width <= 0) return;
  const next = ((rect.right - clientX) / rect.width) * 100;
  workbenchDevtoolsWidthPercent.value = clamp(next, WORKBENCH_DEVTOOLS_WIDTH_MIN, WORKBENCH_DEVTOOLS_WIDTH_MAX);
}

function resizeWorkbenchDevtoolsHeightFromClientY(clientY: number): void {
  const main = document.querySelector<HTMLElement>(".iv__main--workbench");
  if (!main) return;
  const rect = main.getBoundingClientRect();
  if (rect.height <= 0) return;
  const next = ((rect.bottom - clientY) / rect.height) * 100;
  workbenchDevtoolsHeightPercent.value = clamp(next, WORKBENCH_DEVTOOLS_HEIGHT_MIN, WORKBENCH_DEVTOOLS_HEIGHT_MAX);
}

function stopResizeListeners(): void {
  document.removeEventListener("pointermove", onColumnResizeMove);
  document.removeEventListener("pointerup", stopResizeListeners);
  document.removeEventListener("pointercancel", stopResizeListeners);
  document.removeEventListener("pointermove", onRowResizeMove);
  document.removeEventListener("pointermove", onWorkbenchMediaResizeMove);
  document.removeEventListener("pointermove", onWorkbenchDevtoolsWidthResizeMove);
  document.removeEventListener("pointermove", onWorkbenchDevtoolsHeightResizeMove);
}

function onColumnResizeMove(e: PointerEvent): void {
  resizeColumnFromClientX(e.clientX);
}

function onRowResizeMove(e: PointerEvent): void {
  resizeRowFromClientY(e.clientY);
}

function onWorkbenchMediaResizeMove(e: PointerEvent): void {
  resizeWorkbenchMediaFromClientX(e.clientX);
}

function onWorkbenchDevtoolsWidthResizeMove(e: PointerEvent): void {
  resizeWorkbenchDevtoolsWidthFromClientX(e.clientX);
}

function onWorkbenchDevtoolsHeightResizeMove(e: PointerEvent): void {
  resizeWorkbenchDevtoolsHeightFromClientY(e.clientY);
}

function startColumnResize(e: PointerEvent): void {
  e.preventDefault();
  (e.currentTarget as HTMLElement | null)?.setPointerCapture?.(e.pointerId);
  resizeColumnFromClientX(e.clientX);
  document.addEventListener("pointermove", onColumnResizeMove);
  document.addEventListener("pointerup", stopResizeListeners, { once: true });
  document.addEventListener("pointercancel", stopResizeListeners, { once: true });
}

function startRowResize(e: PointerEvent): void {
  e.preventDefault();
  (e.currentTarget as HTMLElement | null)?.setPointerCapture?.(e.pointerId);
  resizeRowFromClientY(e.clientY);
  document.addEventListener("pointermove", onRowResizeMove);
  document.addEventListener("pointerup", stopResizeListeners, { once: true });
  document.addEventListener("pointercancel", stopResizeListeners, { once: true });
}

function startWorkbenchMediaResize(e: PointerEvent): void {
  e.preventDefault();
  (e.currentTarget as HTMLElement | null)?.setPointerCapture?.(e.pointerId);
  resizeWorkbenchMediaFromClientX(e.clientX);
  document.addEventListener("pointermove", onWorkbenchMediaResizeMove);
  document.addEventListener("pointerup", stopResizeListeners, { once: true });
  document.addEventListener("pointercancel", stopResizeListeners, { once: true });
}

function startWorkbenchDevtoolsWidthResize(e: PointerEvent): void {
  e.preventDefault();
  (e.currentTarget as HTMLElement | null)?.setPointerCapture?.(e.pointerId);
  resizeWorkbenchDevtoolsWidthFromClientX(e.clientX);
  document.addEventListener("pointermove", onWorkbenchDevtoolsWidthResizeMove);
  document.addEventListener("pointerup", stopResizeListeners, { once: true });
  document.addEventListener("pointercancel", stopResizeListeners, { once: true });
}

function startWorkbenchDevtoolsHeightResize(e: PointerEvent): void {
  e.preventDefault();
  (e.currentTarget as HTMLElement | null)?.setPointerCapture?.(e.pointerId);
  resizeWorkbenchDevtoolsHeightFromClientY(e.clientY);
  document.addEventListener("pointermove", onWorkbenchDevtoolsHeightResizeMove);
  document.addEventListener("pointerup", stopResizeListeners, { once: true });
  document.addEventListener("pointercancel", stopResizeListeners, { once: true });
}

function onColumnResizeKeydown(e: KeyboardEvent): void {
  const step = e.shiftKey ? 10 : 4;
  if (e.key === "ArrowLeft") {
    traceWidthPercent.value = clamp(traceWidthPercent.value + step, TRACE_WIDTH_MIN, TRACE_WIDTH_MAX);
    e.preventDefault();
  } else if (e.key === "ArrowRight") {
    traceWidthPercent.value = clamp(traceWidthPercent.value - step, TRACE_WIDTH_MIN, TRACE_WIDTH_MAX);
    e.preventDefault();
  } else if (e.key === "Home") {
    traceWidthPercent.value = TRACE_WIDTH_MAX;
    e.preventDefault();
  } else if (e.key === "End") {
    traceWidthPercent.value = TRACE_WIDTH_MIN;
    e.preventDefault();
  }
}

function onRowResizeKeydown(e: KeyboardEvent): void {
  const step = e.shiftKey ? 10 : 4;
  if (e.key === "ArrowUp") {
    diagramHeightPercent.value = clamp(diagramHeightPercent.value - step, DIAGRAM_HEIGHT_MIN, DIAGRAM_HEIGHT_MAX);
    e.preventDefault();
  } else if (e.key === "ArrowDown") {
    diagramHeightPercent.value = clamp(diagramHeightPercent.value + step, DIAGRAM_HEIGHT_MIN, DIAGRAM_HEIGHT_MAX);
    e.preventDefault();
  } else if (e.key === "Home") {
    diagramHeightPercent.value = DIAGRAM_HEIGHT_MIN;
    e.preventDefault();
  } else if (e.key === "End") {
    diagramHeightPercent.value = DIAGRAM_HEIGHT_MAX;
    e.preventDefault();
  }
}

function onWorkbenchMediaResizeKeydown(e: KeyboardEvent): void {
  const step = e.shiftKey ? 10 : 4;
  if (e.key === "ArrowLeft") {
    workbenchMediaWidthPercent.value = clamp(
      workbenchMediaWidthPercent.value - step,
      WORKBENCH_MEDIA_WIDTH_MIN,
      WORKBENCH_MEDIA_WIDTH_MAX,
    );
    e.preventDefault();
  } else if (e.key === "ArrowRight") {
    workbenchMediaWidthPercent.value = clamp(
      workbenchMediaWidthPercent.value + step,
      WORKBENCH_MEDIA_WIDTH_MIN,
      WORKBENCH_MEDIA_WIDTH_MAX,
    );
    e.preventDefault();
  } else if (e.key === "Home") {
    workbenchMediaWidthPercent.value = WORKBENCH_MEDIA_WIDTH_MIN;
    e.preventDefault();
  } else if (e.key === "End") {
    workbenchMediaWidthPercent.value = WORKBENCH_MEDIA_WIDTH_MAX;
    e.preventDefault();
  }
}

function onWorkbenchDevtoolsWidthResizeKeydown(e: KeyboardEvent): void {
  const step = e.shiftKey ? 10 : 4;
  if (e.key === "ArrowLeft") {
    workbenchDevtoolsWidthPercent.value = clamp(
      workbenchDevtoolsWidthPercent.value + step,
      WORKBENCH_DEVTOOLS_WIDTH_MIN,
      WORKBENCH_DEVTOOLS_WIDTH_MAX,
    );
    e.preventDefault();
  } else if (e.key === "ArrowRight") {
    workbenchDevtoolsWidthPercent.value = clamp(
      workbenchDevtoolsWidthPercent.value - step,
      WORKBENCH_DEVTOOLS_WIDTH_MIN,
      WORKBENCH_DEVTOOLS_WIDTH_MAX,
    );
    e.preventDefault();
  } else if (e.key === "Home") {
    workbenchDevtoolsWidthPercent.value = WORKBENCH_DEVTOOLS_WIDTH_MAX;
    e.preventDefault();
  } else if (e.key === "End") {
    workbenchDevtoolsWidthPercent.value = WORKBENCH_DEVTOOLS_WIDTH_MIN;
    e.preventDefault();
  }
}

function onWorkbenchDevtoolsHeightResizeKeydown(e: KeyboardEvent): void {
  const step = e.shiftKey ? 10 : 4;
  if (e.key === "ArrowUp") {
    workbenchDevtoolsHeightPercent.value = clamp(
      workbenchDevtoolsHeightPercent.value + step,
      WORKBENCH_DEVTOOLS_HEIGHT_MIN,
      WORKBENCH_DEVTOOLS_HEIGHT_MAX,
    );
    e.preventDefault();
  } else if (e.key === "ArrowDown") {
    workbenchDevtoolsHeightPercent.value = clamp(
      workbenchDevtoolsHeightPercent.value - step,
      WORKBENCH_DEVTOOLS_HEIGHT_MIN,
      WORKBENCH_DEVTOOLS_HEIGHT_MAX,
    );
    e.preventDefault();
  } else if (e.key === "Home") {
    workbenchDevtoolsHeightPercent.value = WORKBENCH_DEVTOOLS_HEIGHT_MAX;
    e.preventDefault();
  } else if (e.key === "End") {
    workbenchDevtoolsHeightPercent.value = WORKBENCH_DEVTOOLS_HEIGHT_MIN;
    e.preventDefault();
  }
}

function onFreshnessReloaded(prevStateExists: boolean): void {
  reloadWarning.value = prevStateExists ? null : "current state removed; staying put";
}

function onFreshnessError(msg: string): void {
  reloadWarning.value = msg;
}

async function loadSession(sessionId: string): Promise<void> {
  if (!source) source = createDataSource();
  sourceCanDriveOperation.value = !(source instanceof SnapshotSource);
  // hydrate resets prior session state, loads session/app/mermaid/trace, and
  // opens the live subscription; loadInitialView seeds currentView + the
  // opening agent transcript entry.
  await store.hydrate(source, sessionId);
  await store.loadInitialView(source, sessionId);
  await maybeSeedProposalsFromQuery();
  await maybeTeleportFromQuery(sessionId);
  await maybeShowChatFromQuery(sessionId);
  startGitHubInboxPolling(sessionId);
}

function startGitHubInboxPolling(sessionId: string): void {
  stopGitHubInboxPolling();
  if (!canSyncGitHubInbox(source)) return;
  void inbox.syncGitHub(source, sessionId, undefined, { silent: true });
  githubInboxTimer = setInterval(() => {
    if (!canSyncGitHubInbox(source)) return;
    void inbox.syncGitHub(source, sessionId, undefined, { silent: true });
  }, GITHUB_INBOX_REFRESH_INTERVAL_MS);
}

function stopGitHubInboxPolling(): void {
  if (!githubInboxTimer) return;
  clearInterval(githubInboxTimer);
  githubInboxTimer = null;
}

/**
 * Inbox deep-link: if the route carries `?notif=<id>`, teleport the session to
 * that notification's target room, apply the resulting view, mark the
 * notification read, then clear the query param via router.replace so a refresh
 * doesn't re-teleport. A non-teleportable / unknown id rejects with -32000 — we
 * surface it as a soft error and still clear the param. Runs AFTER hydrate so
 * the run store is ready to receive the TurnResult.
 */
async function maybeTeleportFromQuery(sessionId: string): Promise<void> {
  // Guard for mounts without a router (some unit tests mount the view bare).
  if (!route || !router) return;
  const raw = route.query.notif;
  const notifId = Array.isArray(raw) ? raw[0] : raw;
  if (!notifId || typeof notifId !== "string") return;
  const live = new LiveSource("/");
  try {
    const result = await live.teleport(sessionId, notifId);
    store.applyTurnResult(result);
  } catch (e) {
    error.value = e instanceof Error ? e.message : String(e);
  }
  void inbox.markRead(live, sessionId, notifId);
  // Clear the param so a page refresh doesn't re-fire the teleport.
  const q = { ...route.query };
  delete q.notif;
  await router.replace({ path: route.path, query: q });
}

async function maybeShowChatFromQuery(sessionId: string): Promise<void> {
  if (!route) return;
  const raw = route.query.chat;
  const chatID = Array.isArray(raw) ? raw[0] : raw;
  if (!chatID || typeof chatID !== "string") {
    focusedChatSeq += 1;
    focusedChat.value = null;
    focusedChatID.value = "";
    focusedChatError.value = null;
    focusedChatLoading.value = false;
    return;
  }
  const seq = ++focusedChatSeq;
  focusedChatID.value = chatID;
  focusedChatLoading.value = true;
  focusedChatError.value = null;
  const live = new LiveSource("/");
  try {
    const result = await live.showChat(sessionId, chatID);
    if (seq !== focusedChatSeq) return;
    focusedChat.value = result;
  } catch (e) {
    if (seq !== focusedChatSeq) return;
    focusedChat.value = null;
    focusedChatError.value = e instanceof Error ? e.message : String(e);
  } finally {
    if (seq === focusedChatSeq) {
      focusedChatLoading.value = false;
    }
  }
}

async function maybeSeedProposalsFromQuery(): Promise<void> {
  if (!route || !router) return;
  const raw = route.query.proposal;
  const encoded = Array.isArray(raw) ? raw : raw ? [raw] : [];
  if (encoded.length === 0) return;

  for (const item of encoded) {
    if (typeof item !== "string") continue;
    try {
      proposals.push(JSON.parse(item) as Proposal);
    } catch {
      /* malformed query seed — ignore (deterministic render/demo path only) */
    }
  }

  const q = { ...route.query };
  delete q.proposal;
  await router.replace({ path: route.path, query: q });
}

async function maybeClearDraftFromQuery(): Promise<void> {
  if (!route || !router || route.query.draft == null) return;
  const q = { ...route.query };
  delete q.draft;
  await router.replace({ path: route.path, query: q });
}

function queryString(value: unknown): string {
  if (Array.isArray(value)) return typeof value[0] === "string" ? value[0] : "";
  return typeof value === "string" ? value : "";
}

async function clearFocusedChat(): Promise<void> {
  focusedChatSeq += 1;
  focusedChat.value = null;
  focusedChatID.value = "";
  focusedChatError.value = null;
  focusedChatLoading.value = false;
  if (!route || !router) return;
  const q = { ...route.query };
  delete q.chat;
  await router.replace({ path: route.path, query: q });
}

onMounted(() => {
  // Viewing a session spends the per-tab auto-nav convenience: if this view is
  // the tab's first mount (a pasted/bookmarked /s/:id/chat link, or the push
  // right after starting a session), the home screen must NOT later bounce the
  // user back in when they click "← Stories" with one live session.
  markAutoNavDone();
  window.addEventListener("kitsoki:pin-media", onPinMedia);
  void loadSession(props.sessionId).then(() => maybeClearDraftFromQuery());

  // Demo / tour test hook: submit an explicit intent through THIS view's own
  // store path (the same code path InputBar's @intent uses), so the chat +
  // InputBar re-render reactively — unlike an out-of-band session.submit RPC,
  // which advances the engine but leaves this view stale. Mirrors the
  // window.__startTourWithSteps hook the tour video specs rely on. Used to
  // drive semantic-routing rooms (no intent buttons) on-camera deterministically
  // in the no-LLM --flow posture. Inert unless a spec calls it.
  // `displayLabel` lets a demo render the operator's REAL verbatim utterance as
  // the user transcript bubble (e.g. the mined "rebase onto main and resolve the
  // conflicts") while the engine deterministically processes the resolved intent
  // — exactly the shape a live LLM session leaves behind (utterance + resolved
  // intent), so the no-LLM video shows real input, not a synthetic intent name.
  (window as unknown as {
    __kitsokiSubmitIntent?: (
      name: string,
      slots?: Record<string, unknown>,
      displayLabel?: string,
    ) => Promise<void>;
  }).__kitsokiSubmitIntent = async (
    name: string,
    slots: Record<string, unknown> = {},
    displayLabel?: string,
  ) => {
    if (!source) return;
    await runTurn(() =>
      displayLabel === undefined
        ? store.submitIntent(source!, props.sessionId, name, slots)
        : store.submitIntent(source!, props.sessionId, name, slots, displayLabel),
    );
  };

  // __kitsokiSendText drives a FREE-TEXT turn through the store's sendText
  // (session.turn → the real routing tiers), so a demo types the operator's
  // verbatim utterance and the engine ROUTES it (semantic tier, no LLM) rather
  // than receiving a pre-resolved intent. This is what makes the routing chip
  // light up on-camera: the turn carries genuine routed_by/match_type
  // provenance. Mirrors __kitsokiSubmitIntent; inert unless a spec calls it.
  (window as unknown as {
    __kitsokiSendText?: (text: string) => Promise<void>;
  }).__kitsokiSendText = async (text: string) => {
    if (!source) return;
    await runTurn(() => store.sendText(source!, props.sessionId, text));
  };

  // Bind the proposals store to the live source so an accepted proposal resolves
  // over answer_question, and expose the deterministic seed seam (mirrors
  // __pushOperatorQuestion) so a no-LLM spec can populate the proposals badge
  // without a real miner. Inert unless a spec calls it.
  if (source instanceof LiveSource) proposals.init(source);
  (window as unknown as {
    __pushProposal?: (proposalJson: string) => void;
  }).__pushProposal = (proposalJson: string) => {
    try {
      proposals.push(JSON.parse(proposalJson) as Proposal);
    } catch {
      /* malformed JSON — ignore (deterministic test/demo driver only) */
    }
  };
});

// Switching directly between two /s/:sessionId/chat routes reuses this
// component (only the param changes), so onMounted never re-fires. Re-load on
// sessionId change so the new session's chat isn't left showing the old one.
watch(
  () => props.sessionId,
  (next) => {
    void loadSession(next);
  }
);

watch(
  () => route?.query?.chat,
  () => {
    void maybeShowChatFromQuery(props.sessionId);
  }
);

watch(
  mediaItems,
  (items) => {
    if (items.length === 0) {
      workbenchEnabled.value = false;
      selectedMediaKey.value = "";
      return;
    }
    if (!items.some((item) => item.key === selectedMediaKey.value)) {
      selectedMediaKey.value = items.at(-1)?.key ?? "";
    }
  },
  { flush: "post" },
);

onUnmounted(() => {
  window.removeEventListener("kitsoki:pin-media", onPinMedia);
  stopResizeListeners();
  stopGitHubInboxPolling();
  store.teardown();
  proposals.teardown();
  delete (window as unknown as { __kitsokiSubmitIntent?: unknown }).__kitsokiSubmitIntent;
  delete (window as unknown as { __kitsokiSendText?: unknown }).__kitsokiSendText;
  delete (window as unknown as { __pushProposal?: unknown }).__pushProposal;
});

/**
 * Run a write action with the pending guard. The store actions push the
 * user/agent transcript entries and apply the result; we only manage the
 * in-flight flag and surface transport-level errors here. Guard rejections /
 * clarifications ride back inside the TurnResult and are rendered as agent
 * transcript entries, so they are NOT errors.
 */
async function runTurn(fn: () => Promise<unknown>): Promise<void> {
  if (pending.value || !source || store.terminal) return;
  pending.value = true;
  error.value = null;
  try {
    await fn();
  } catch (e) {
    // A cancelled turn is a clean operator action, not an error: the server
    // aborted it and persisted nothing, so reset to idle WITHOUT a red toast.
    if (e instanceof TurnCancelledError) {
      // no-op — the pending bubble clears in finally and the room is unchanged
    } else {
      error.value = e instanceof Error ? e.message : String(e);
    }
  } finally {
    pending.value = false;
    cancelling.value = false;
    if (canListWork(source)) {
      await inbox.refreshWork(source);
    }
  }
}

/**
 * Stop the in-flight turn. Fires runstatus.session.cancel, which aborts the
 * agent server-side (not just the frontend); the in-flight turnStream then
 * rejects with TurnCancelledError and runTurn resets to idle. Guarded so a
 * double-click can't fire two cancels.
 */
async function onCancel(): Promise<void> {
  if (!source || !pending.value || cancelling.value) return;
  if (!(source instanceof LiveSource)) return;
  cancelling.value = true;
  try {
    await source.cancelTurn(props.sessionId);
  } catch {
    // The cancel RPC itself failing is non-fatal: if the turn is already
    // finishing, the stream's terminal frame still resets the UI. Re-enable the
    // button so the operator can retry.
    cancelling.value = false;
  }
}

/**
 * Raw-text submission from semantic routing rooms (the free-text textarea).
 * Routes via session.turn so the semantic router handles natural-language
 * dispatch. Text-slot intent forms use onIntent / session.submit instead.
 */
function onSend(text: string, _intentName: string): void {
  if (!source) return;
  void runTurn(() => store.sendText(source!, props.sessionId, text));
}

function onIntent(name: string, slots: Record<string, unknown>, displayLabel?: string): void {
  if (!source) return;
  void runTurn(() =>
    displayLabel === undefined
      ? store.submitIntent(source!, props.sessionId, name, slots)
      : store.submitIntent(source!, props.sessionId, name, slots, displayLabel),
  );
}

// Rewind one CRR decision from its route-receipt chip (re-dispatch under the
// journaled class). Routes through runTurn for the same in-flight guard +
// error-banner behaviour as a normal turn; the chip disables the control for
// non-rewindable (intent-class) receipts so it never reaches here.
function onRewind(decisionId: string): void {
  if (!source) return;
  void runTurn(() => store.rewindRoute(source!, props.sessionId, decisionId));
}

function onDriveOperation(): void {
  if (!source || !canDriveOperation.value) return;
  drivingOperation.value = true;
  void runTurn(() => store.driveOperation(source!, props.sessionId)).finally(() => {
    drivingOperation.value = false;
  });
}

// Routing-feedback thumbs up/down (WS-C C4): fire-and-forget, no in-flight
// guard needed since it never advances the turn.
function onFeedback(entry: TranscriptEntry, verdict: "up" | "down"): void {
  if (!source) return;
  void store.sendRoutingFeedback(source, props.sessionId, entry, verdict);
}

// ---- trace interactions (mirror RunView observer behavior) ----
function onNodeSelect(_nodeId: string, nodeRef: NodeRef): void {
  if (nodeRef.kind === "state") {
    store.setHighlightedStatePaths([nodeRef.ref]);
  }
}
function onPhaseSelect(_phaseId: string, roomRefs: string[]): void {
  store.setHighlightedStatePaths(roomRefs);
}
function onClearHighlight(): void {
  store.setHighlightedStatePaths([]);
}
function onEventSelect(index: number): void {
  store.selectEvent(index);
}
</script>

<style scoped>
.iv {
  display: flex;
  flex-direction: column;
  height: 100vh;
  background: var(--k-bg-deep, #0a1120);
  color: var(--k-fg, #e2e8f0);
  overflow: hidden;
}

.iv__loading {
  display: flex;
  align-items: center;
  justify-content: center;
  height: 100%;
  color: var(--k-fg-muted, #64748b);
  font-size: 1rem;
}

/* ---- Top bar ---- */
.iv__topbar {
  display: flex;
  align-items: center;
  gap: 0.75rem;
  padding: 0.55rem 1rem;
  background: var(--k-bg-widget, #0f172a);
  border-bottom: 1px solid var(--k-border, #1e293b);
  flex-shrink: 0;
  font-size: 0.8125rem;
}

.iv__back {
  color: var(--k-fg-accent, #60a5fa);
  text-decoration: none;
}
.iv__back:hover {
  text-decoration: underline;
}

.iv__app-id {
  font-weight: 600;
  color: var(--k-fg, #e2e8f0);
}

.iv__sep {
  color: var(--k-fg-subtle, #334155);
}

.iv__current-state {
  font-family: ui-monospace, monospace;
  font-size: 0.775rem;
  color: var(--k-fg-code, #7dd3fc);
}

.iv__state-badge {
  display: inline-block;
  padding: 0.1rem 0.45rem;
  border-radius: 999px;
  font-size: 0.7rem;
  font-weight: 600;
}
.iv__state-badge--live {
  background: var(--k-success-bg, #14532d);
  color: var(--k-success, #86efac);
}
.iv__state-badge--done {
  background: var(--k-bg-input, #1e293b);
  color: var(--k-fg-muted, #64748b);
}

.iv__usage {
  font-family: ui-monospace, monospace;
  font-size: 0.75rem;
  color: #a3e635;
  background: #1a2e05;
  border: 1px solid #3f6212;
  border-radius: 4px;
  padding: 0.1rem 0.45rem;
  white-space: nowrap;
}
/* No spend yet — the deterministic-run steady state. Brighter green so the
   "this is free" signal reads at a glance while the operator drives. */
.iv__usage--zero {
  color: #bef264;
  border-color: #4d7c0f;
}

.iv__harness {
  display: inline-flex;
  align-items: center;
  gap: 6px;
}
.iv__harness-select {
  background: #111c33;
  color: #cbd5e1;
  border: 1px solid #2b3a55;
  border-radius: 4px;
  font-size: 12px;
  padding: 2px 4px;
  max-width: 200px;
}
.iv__harness-select:hover {
  border-color: #3b82f6;
}
.iv__trace-toggle {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  background: #111c33;
  color: #cbd5e1;
  border: 1px solid #2b3a55;
  border-radius: 4px;
  font-size: 0.75rem;
  font-family: inherit;
  padding: 0.18rem 0.5rem;
  cursor: pointer;
  white-space: nowrap;
}

.iv__trace-toggle:hover {
  background: #172544;
  border-color: #3b82f6;
}

.iv__workbench-toggle,
.iv__workbench-action {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  background: #10251f;
  color: #bbf7d0;
  border: 1px solid #166534;
  border-radius: 4px;
  font-size: 0.75rem;
  font-family: inherit;
  padding: 0.18rem 0.5rem;
  cursor: pointer;
  white-space: nowrap;
}

.iv__workbench-toggle:hover,
.iv__workbench-action:hover {
  background: #123626;
  border-color: #22c55e;
}

/* ---- Main row ---- */
.iv__reload-warning {
  flex-shrink: 0;
  padding: 0.25rem 1rem;
  font-size: 0.8rem;
  background: #1c1107;
  border-bottom: 1px solid #92400e;
  color: var(--k-warning, #fcd34d);
}

.iv__reconnecting {
  flex-shrink: 0;
  display: flex;
  align-items: center;
  gap: 0.5rem;
  padding: 0.25rem 1rem;
  font-size: 0.8rem;
  background: #0b1a24;
  border-bottom: 1px solid #1e4a63;
  color: var(--k-info, #7dd3fc);
}

.iv__reconnecting-dot {
  width: 0.5rem;
  height: 0.5rem;
  border-radius: 50%;
  background: currentColor;
  animation: iv-reconnecting-pulse 1s ease-in-out infinite;
}

@keyframes iv-reconnecting-pulse {
  0%,
  100% {
    opacity: 0.3;
  }
  50% {
    opacity: 1;
  }
}

.iv__operation {
  flex-shrink: 0;
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: 0.5rem;
  padding: 0.34rem 1rem;
  font-size: 0.78rem;
  background: #0f172a;
  border-bottom: 1px solid #334155;
  color: var(--k-fg, #e2e8f0);
  min-width: 0;
}

.iv__operation-dot {
  width: 0.55rem;
  height: 0.55rem;
  border-radius: 50%;
  background: #38bdf8;
  box-shadow: 0 0 0 2px rgba(56, 189, 248, 0.16);
}

.iv__operation-label {
  color: var(--k-fg-muted, #94a3b8);
  font-size: 0.66rem;
  font-weight: 700;
  letter-spacing: 0;
  text-transform: uppercase;
  white-space: nowrap;
}

.iv__operation-title,
.iv__operation-route,
.iv__operation-detail,
.iv__operation-artifact {
  min-width: 4rem;
  max-width: 100%;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.iv__operation-title {
  flex: 0 1 auto;
}

.iv__operation-route {
  flex: 1 1 10rem;
}

.iv__operation-status {
  border: 1px solid #0e7490;
  border-radius: 999px;
  color: #bae6fd;
  background: #082f49;
  font-size: 0.68rem;
  font-weight: 700;
  padding: 0.08rem 0.45rem;
  white-space: nowrap;
}

.iv__operation-actions {
  margin-left: auto;
  display: flex;
  align-items: center;
  gap: 0.4rem;
}

.iv__operation-action {
  border: 1px solid #0e7490;
  border-radius: 0.375rem;
  background: #082f49;
  color: #bae6fd;
  display: inline-flex;
  align-items: center;
  justify-content: center;
  font: inherit;
  font-size: 0.72rem;
  font-weight: 700;
  line-height: 1.2;
  padding: 0.16rem 0.55rem;
  text-decoration: none;
  white-space: nowrap;
  cursor: pointer;
}

.iv__operation-action:hover:not(:disabled) {
  background: #0c4a6e;
}

.iv__operation-action:disabled {
  cursor: default;
  opacity: 0.55;
}

.iv__operation-route,
.iv__operation-detail,
.iv__operation-artifact {
  color: var(--k-fg-muted, #94a3b8);
  font-size: 0.72rem;
}

.iv__operation-detail {
  flex: 1 1 14rem;
  color: #fde68a;
}

.iv__operation-facts {
  display: flex;
  flex: 1 1 100%;
  flex-wrap: wrap;
  gap: 0.25rem;
  min-width: 0;
  padding-left: 1.55rem;
}

.iv__operation-fact {
  display: inline-flex;
  align-items: baseline;
  gap: 0.2rem;
  max-width: 100%;
  border: 1px solid #334155;
  border-radius: 4px;
  padding: 0.06rem 0.34rem;
  color: var(--k-fg-muted, #94a3b8);
  font-size: 0.68rem;
  line-height: 1.25;
  overflow-wrap: anywhere;
}

.iv__operation-fact-label {
  color: var(--k-fg-subtle, #64748b);
  font-size: 0.58rem;
  font-weight: 700;
  letter-spacing: 0;
  text-transform: uppercase;
}

.iv__operation--completed .iv__operation-dot {
  background: #22c55e;
  box-shadow: 0 0 0 2px rgba(34, 197, 94, 0.16);
}

.iv__operation--completed .iv__operation-status {
  border-color: #15803d;
  background: #052e16;
  color: #bbf7d0;
}

.iv__operation--failed .iv__operation-dot {
  background: #f87171;
  box-shadow: 0 0 0 2px rgba(248, 113, 113, 0.16);
}

.iv__operation--failed .iv__operation-status {
  border-color: #991b1b;
  background: #450a0a;
  color: #fecaca;
}

.iv__operation--waiting .iv__operation-dot {
  background: #f59e0b;
  box-shadow: 0 0 0 2px rgba(245, 158, 11, 0.16);
}

.iv__operation--waiting .iv__operation-status {
  border-color: #b45309;
  background: #451a03;
  color: #fde68a;
}

.iv__main {
  display: flex;
  flex: 1;
  min-height: 0;
  gap: 0;
}

.iv__workbench-bar {
  flex-shrink: 0;
  display: flex;
  align-items: center;
  gap: 0.6rem;
  padding: 0.4rem 0.75rem;
  background: #0c1627;
  border-bottom: 1px solid var(--k-border, #1e293b);
  min-width: 0;
}

.iv__workbench-field {
  display: inline-flex;
  align-items: center;
  gap: 0.4rem;
  min-width: 16rem;
  color: var(--k-fg-muted, #94a3b8);
  font-size: 0.74rem;
}

.iv__workbench-select {
  min-width: 0;
  width: 100%;
  max-width: 22rem;
  background: #111c33;
  color: #e2e8f0;
  border: 1px solid #2b3a55;
  border-radius: 4px;
  font-size: 0.74rem;
  padding: 0.18rem 0.35rem;
}

.iv__segmented {
  display: inline-flex;
  border: 1px solid #2b3a55;
  border-radius: 5px;
  overflow: hidden;
  flex: 0 0 auto;
}

.iv__segmented-btn,
.iv__devtools-tab,
.iv__pane-action {
  background: #111c33;
  color: #cbd5e1;
  border: 0;
  border-right: 1px solid #2b3a55;
  font: inherit;
  font-size: 0.72rem;
  padding: 0.22rem 0.45rem;
  cursor: pointer;
}

.iv__segmented-btn:last-child,
.iv__devtools-tab:last-child {
  border-right: 0;
}

.iv__segmented-btn[aria-pressed="true"],
.iv__devtools-tab[aria-selected="true"] {
  background: #1d4ed8;
  color: #eff6ff;
}

.iv__main--workbench {
  display: grid;
  grid-template-columns: minmax(18rem, 42%) 0.55rem minmax(20rem, 1fr) 0.55rem minmax(16rem, 28%);
}

.iv__main--workbench-horizontal,
.iv__main--devtools-bottom {
  grid-template-columns: minmax(18rem, 42%) 0.55rem minmax(20rem, 1fr);
  grid-template-rows: minmax(0, 1fr) 0.55rem minmax(12rem, 34%);
}

.iv__main--workbench-horizontal .iv__devtools,
.iv__main--devtools-bottom .iv__devtools {
  grid-row: 3;
  grid-column: 1 / -1;
}

.iv__main--devtools-floating {
  grid-template-columns: minmax(18rem, 42%) 0.55rem minmax(20rem, 1fr);
}

.iv__media-pane,
.iv__devtools {
  display: flex;
  min-width: 0;
  min-height: 0;
  flex-direction: column;
  border-right: 1px solid var(--k-border, #1e293b);
  background: #0b1220;
}

.iv__media-pane {
  overflow: hidden;
}

.iv__pane-header {
  flex-shrink: 0;
  display: flex;
  align-items: center;
  gap: 0.55rem;
  min-height: 2.25rem;
  padding: 0.42rem 0.6rem;
  border-bottom: 1px solid var(--k-border, #1e293b);
  background: #0f172a;
  color: #e2e8f0;
  font-size: 0.78rem;
  font-weight: 650;
}

.iv__pane-subtitle {
  min-width: 0;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  color: var(--k-fg-muted, #94a3b8);
  font-weight: 500;
}

.iv__media-stage {
  flex: 1 1 auto;
  min-height: 0;
  overflow: auto;
  padding: 0.75rem;
}

.iv__media-stage :deep(.ve-media),
.iv__media-stage :deep(.ve-media-video),
.iv__media-stage :deep(.ve-media-image),
.iv__media-stage :deep(.ve-media-iframe) {
  height: 100%;
  max-height: none;
}

.iv__media-stage :deep(.ve-media-iframe) {
  min-height: 36rem;
}

.iv__devtools {
  overflow: hidden;
}

.iv__devtools-body {
  flex: 1 1 auto;
  min-height: 0;
  display: flex;
  flex-direction: column;
}

.iv__devtools-body :deep(.state-diagram),
.iv__devtools-body :deep(.trace-timeline) {
  flex: 1;
  height: 100%;
  min-height: 0;
}

.iv__devtools-tabs {
  display: inline-flex;
  border: 1px solid #2b3a55;
  border-radius: 5px;
  overflow: hidden;
}

.iv__pane-action {
  margin-left: auto;
  border: 1px solid #2b3a55;
  border-radius: 4px;
}

.iv__floating-devtools {
  position: fixed;
  right: 1rem;
  bottom: 1rem;
  z-index: 850;
  width: min(44rem, calc(100vw - 2rem));
  height: min(34rem, calc(100vh - 6rem));
  display: flex;
  flex-direction: column;
  resize: both;
  overflow: auto;
  background: #0b1220;
  border: 1px solid #2b3a55;
  border-radius: 6px;
  box-shadow: 0 18px 48px rgba(0, 0, 0, 0.45);
}

.iv__main--trace-collapsed .iv__chat {
  border-right: 0;
}

/* LEFT: chat column */
.iv__chat {
  display: flex;
  flex-direction: column;
  flex: 1 1 46%;
  min-width: 0;
  min-height: 0;
  border-right: 1px solid var(--k-border, #1e293b);
  background: var(--k-bg-inset, #0f1115);
}

.iv__transcript {
  flex: 1 1 auto;
  min-height: 0;
}

.iv__chat-media-context {
  flex-shrink: 0;
  display: grid;
  grid-template-columns: auto minmax(0, 1fr) auto;
  align-items: center;
  gap: 0.5rem;
  padding: 0.48rem 0.75rem;
  background: #0d1b2a;
  border-bottom: 1px solid #1f3a5f;
  color: var(--k-fg, #e2e8f0);
  font-size: 0.76rem;
}

.iv__chat-media-context-label,
.iv__chat-media-context-hint {
  color: var(--k-fg-muted, #94a3b8);
  font-size: 0.68rem;
  white-space: nowrap;
}

.iv__chat-media-context strong {
  min-width: 0;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.iv__focused-chat {
  flex-shrink: 0;
  padding: 0.55rem 0.8rem;
  background: #111827;
  border-bottom: 1px solid var(--k-border, #1e293b);
  display: grid;
  gap: 0.3rem;
}

.iv__focused-chat-head {
  display: flex;
  align-items: center;
  gap: 0.45rem;
  min-width: 0;
  font-size: 0.78rem;
}

.iv__focused-chat-head strong {
  min-width: 0;
  overflow-wrap: anywhere;
}

.iv__focused-chat-label {
  color: var(--k-fg-accent, #60a5fa);
  font-size: 0.68rem;
  font-weight: 700;
  text-transform: uppercase;
}

.iv__focused-chat-close {
  margin-left: auto;
  background: transparent;
  border: 0;
  color: var(--k-fg-muted, #94a3b8);
  cursor: pointer;
  font-size: 0.7rem;
  padding: 0;
}

.iv__focused-chat-close:hover {
  color: var(--k-fg, #e2e8f0);
  text-decoration: underline;
}

.iv__focused-chat-meta {
  display: flex;
  flex-wrap: wrap;
  gap: 0.45rem;
  color: var(--k-fg-muted, #94a3b8);
  font-family: ui-monospace, SFMono-Regular, Menlo, Monaco, Consolas, "Liberation Mono", monospace;
  font-size: 0.68rem;
}

.iv__focused-chat-messages {
  display: grid;
  gap: 0.2rem;
}

.iv__focused-chat-message {
  display: grid;
  grid-template-columns: 4.5rem minmax(0, 1fr);
  gap: 0.45rem;
  font-size: 0.72rem;
}

.iv__focused-chat-role {
  color: var(--k-fg-muted, #94a3b8);
  font-weight: 650;
}

.iv__focused-chat-content {
  min-width: 0;
  color: var(--k-fg, #dbeafe);
  overflow-wrap: anywhere;
}

.iv__focused-chat-muted {
  color: var(--k-fg-muted, #94a3b8);
  font-size: 0.72rem;
}

.iv__focused-chat-error {
  color: var(--k-error, #fca5a5);
  font-size: 0.72rem;
}

.iv__done-note {
  padding: 0.6rem 1.1rem;
  font-size: 0.8rem;
  color: var(--k-fg-muted, #64748b);
  background: var(--k-bg-inset, #14171d);
  border-top: 1px solid var(--k-border-subtle, #2a2f3a);
  text-align: center;
}

.iv__error {
  padding: 0.5rem 1.1rem;
  font-size: 0.78rem;
  color: var(--k-error, #fca5a5);
  background: #2a1518;
  border-top: 1px solid #7f1d1d;
}

/* RIGHT: trace column */
.iv__trace {
  display: flex;
  flex-direction: column;
  min-width: 0;
  min-height: 0;
  padding: 0.5rem;
  gap: 0;
  /* anchor for the absolutely-positioned decorative TracePet at the bottom */
  position: relative;
}

/* group the diagram-header actions (pet toggle + hide) on the right */
.iv__panel-actions {
  display: inline-flex;
  align-items: center;
  gap: 0.35rem;
}
.iv__panel-action--pet {
  font-size: 0.85rem;
  line-height: 1;
  filter: grayscale(0.4);
  opacity: 0.65;
}
.iv__panel-action--pet:hover {
  filter: none;
  opacity: 1;
}
.iv__panel-action--pet[aria-pressed="true"] {
  filter: none;
  opacity: 1;
}

.iv__resize-handle {
  flex: 0 0 auto;
  border: 0;
  padding: 0;
  background: transparent;
  cursor: col-resize;
  position: relative;
  touch-action: none;
}

.iv__resize-handle::before {
  content: "";
  position: absolute;
  background: var(--k-border, #1e293b);
}

.iv__resize-handle:hover::before,
.iv__resize-handle:focus-visible::before {
  background: var(--k-fg-accent, #60a5fa);
}

.iv__resize-handle:focus-visible {
  outline: 1px solid var(--k-fg-accent, #60a5fa);
  outline-offset: -1px;
}

.iv__resize-handle--column {
  width: 0.55rem;
  cursor: col-resize;
}

.iv__resize-handle--column::before {
  top: 0;
  bottom: 0;
  left: calc(50% - 1px);
  width: 1px;
}

.iv__resize-handle--row {
  height: 0.55rem;
  cursor: row-resize;
}

.iv__resize-handle--row::before {
  left: 0;
  right: 0;
  top: calc(50% - 1px);
  height: 1px;
}

.iv__main--workbench .iv__resize-handle--workbench-media {
  grid-column: 2;
  grid-row: 1;
}

.iv__main--workbench .iv__resize-handle--workbench-devtools {
  grid-column: 4;
  grid-row: 1;
}

.iv__main--workbench .iv__resize-handle--workbench-devtools-row {
  grid-column: 1 / -1;
  grid-row: 2;
}

.iv__main--workbench .iv__chat,
.iv__main--workbench .iv__media-pane,
.iv__main--workbench .iv__devtools {
  min-width: 0;
  min-height: 0;
}

.iv__main--workbench .iv__media-pane {
  grid-column: 1;
  grid-row: 1;
}

.iv__main--workbench .iv__chat {
  grid-column: 3;
  grid-row: 1;
}

.iv__main--workbench .iv__devtools {
  grid-column: 5;
  grid-row: 1;
}

.iv__main--workbench-horizontal .iv__devtools,
.iv__main--devtools-bottom .iv__devtools {
  grid-column: 1 / -1;
  grid-row: 3;
}

.iv__main--workbench-horizontal .iv__chat,
.iv__main--devtools-bottom .iv__chat,
.iv__main--devtools-floating .iv__chat {
  grid-column: 3;
}

.iv__panel {
  display: flex;
  flex-direction: column;
  overflow: hidden;
  border-radius: 6px;
  min-height: 0;
}

.iv__panel--diagram {
  flex: 0 0 45%;
}

.iv__panel--timeline {
  flex: 1 1 55%;
}

.iv__panel-header {
  font-size: 0.75rem;
  font-weight: 600;
  text-transform: uppercase;
  letter-spacing: 0.05em;
  color: var(--k-fg-muted, #64748b);
  padding: 0.25rem 0;
  flex-shrink: 0;
  display: flex;
  align-items: center;
  gap: 0.5rem;
}

.iv__panel-action {
  margin-left: auto;
  background: transparent;
  border: 0;
  color: var(--k-fg-muted, #94a3b8);
  cursor: pointer;
  font-size: 0.7rem;
  font-family: inherit;
  padding: 0;
  text-transform: none;
  letter-spacing: 0;
}

.iv__panel-action:hover {
  color: var(--k-fg, #e2e8f0);
  text-decoration: underline;
}

.iv__clear-highlight {
  background: #3a2d0e;
  border: 1px solid var(--k-warning, #fbbf24);
  color: #fde68a;
  font-size: 0.65rem;
  text-transform: none;
  letter-spacing: normal;
  padding: 0.1rem 0.4rem;
  border-radius: 999px;
  cursor: pointer;
  font-family: inherit;
}
.iv__clear-highlight:hover {
  background: #4a3a14;
}

.iv__panel--diagram :deep(.state-diagram) {
  flex: 1;
  height: 100%;
}

.iv__panel--timeline :deep(.trace-timeline) {
  flex: 1;
  height: 100%;
  min-height: 0;
}

.iv__empty {
  color: var(--k-fg-subtle, #475569);
  font-size: 0.875rem;
  padding: 1rem;
}

/* ---- Embed layout (VS Code webview): chat ONLY ---- */
/* The chat panel shows just the conversation; Trace and Graph are their own
   dockable windows (the "Kitsoki Surfaces" panels), so the chat fills the width. */
.iv__main--embed .iv__chat {
  flex: 1 1 auto;
}
/* Let the intent-form inputs shrink below their intrinsic width so the row (and
   its Send) always fits the chat column. */
.iv__main--embed .iv__chat :deep(.input-bar__input) {
  min-width: 0;
}

/* ---- Streaming thinking bubble ---- */
.iv__thinking {
  display: flex;
  align-items: flex-start;
  gap: 10px;
  padding: 8px 24px 0;
  max-width: 98%;
}

.iv__thinking-avatar {
  flex: 0 0 auto;
  width: 32px;
  height: 32px;
  border-radius: 50%;
  display: flex;
  align-items: center;
  justify-content: center;
  font-size: 13px;
  font-weight: 600;
  color: #fff;
  background: var(--k-fg-subtle, #475569);
  user-select: none;
}

.iv__thinking-bubble {
  background: var(--k-paper-bg, #f7f8fa);
  color: var(--k-paper-fg, #1f2430);
  border: 1px solid var(--k-paper-border, #d8dbe2);
  border-radius: 12px;
  border-bottom-left-radius: 4px;
  padding: 10px 14px;
  font-size: 14px;
  line-height: 1.5;
  min-width: 120px;
  max-width: 100%;
  overflow-wrap: anywhere;
}

.iv__thinking-head {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 8px;
  margin-bottom: 4px;
}

.iv__thinking-role {
  font-size: 11px;
  font-weight: 600;
  text-transform: uppercase;
  letter-spacing: 0.04em;
  opacity: 0.6;
}

.iv__thinking-stop {
  font-size: 11px;
  font-weight: 600;
  letter-spacing: 0.02em;
  padding: 2px 10px;
  border-radius: 999px;
  border: 1px solid var(--k-danger-border, rgba(248, 113, 113, 0.5));
  background: var(--k-danger-bg, rgba(248, 113, 113, 0.12));
  color: var(--k-danger-fg, #fca5a5);
  cursor: pointer;
  transition: background 0.12s ease, opacity 0.12s ease;
}

.iv__thinking-stop:hover:not(:disabled) {
  background: var(--k-danger-bg-hover, rgba(248, 113, 113, 0.22));
}

.iv__thinking-stop:disabled {
  opacity: 0.55;
  cursor: default;
}

/* The live feed rows (🧠 thoughts + tool calls) come from the shared
   ActivityFeed.vue — the same component the preserved disclosure and the
   meta overlay render, so the stream looks identical everywhere. */

.iv__thinking-dots {
  display: flex;
  gap: 4px;
  font-size: 20px;
  color: var(--k-fg-muted, #94a3b8);
}

@keyframes iv-dot-pulse {
  0%, 80%, 100% { opacity: 0.2; }
  40% { opacity: 1; }
}

.iv__thinking-dots span:nth-child(1) { animation: iv-dot-pulse 1.4s infinite 0s; }
.iv__thinking-dots span:nth-child(2) { animation: iv-dot-pulse 1.4s infinite 0.2s; }
.iv__thinking-dots span:nth-child(3) { animation: iv-dot-pulse 1.4s infinite 0.4s; }
</style>
