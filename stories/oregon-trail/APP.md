# The Oregon Trail

**Version** 0.1.0 · _by MECC (original 1971/1985); ported to Kitsoki_ · License: Spec & rules public domain; this port CC0

## Overview

- App ID: `oregon-trail`
- Entry room: [`intro`](#room-intro)
- Rooms: 114
- Intents: 71
- World variables: 100
- Host allow-list: `host.run`, `host.jobs.answer_clarification`, `host.agent.decide`, `host.agent.ask`, `host.agent.converse`, `host.chat.resolve`, `host.chat.list`, `host.chat.transcript`, `host.chat.create`, `host.chat.fork`, `host.chat.archive`, `host.chat.rename`, `host.chat.suggest_title`, `host.chat.resolve_ref`, `host.transport.post`, `host.run.announce`, `host.run.close`

## State Diagram

```mermaid
flowchart LR
  ended_lost["ended_lost"]
  ended_won["ended_won"]
  fort["fort"]
  fort__compose["fort.compose"]
  fort__describe["fort.describe"]
  fort__done["fort.done"]
  fort__idle["fort.idle"]
  fort__reviewing["fort.reviewing"]
  frontier["frontier"]
  frontier__bandits["frontier.bandits"]
  frontier__bandits__encounter["frontier.bandits.encounter"]
  frontier__scouting["frontier.scouting"]
  general_store["general_store"]
  general_store__compose["general_store.compose"]
  general_store__describe["general_store.describe"]
  general_store__done["general_store.done"]
  general_store__idle["general_store.idle"]
  general_store__reviewing["general_store.reviewing"]
  hunt["hunt"]
  hunt__hunt_done["hunt.hunt_done"]
  hunt__hunt_idle["hunt.hunt_idle"]
  hunt__hunt_running["hunt.hunt_running"]
  inbox["inbox"]
  intro["intro"]
  intro_month["intro_month"]
  intro_party_names["intro_party_names"]
  intro_profession["intro_profession"]
  intro_summary["intro_summary"]
  leg_a_awaiting_reply["leg_a_awaiting_reply"]
  leg_a_error["leg_a_error"]
  leg_a_executing["leg_a_executing"]
  leg_a_executing__event_breakdown["leg_a_executing.event_breakdown"]
  leg_a_executing__event_disease["leg_a_executing.event_disease"]
  leg_a_executing__event_encounter["leg_a_executing.event_encounter"]
  leg_a_executing__event_supply_loss["leg_a_executing.event_supply_loss"]
  leg_a_executing__event_weather["leg_a_executing.event_weather"]
  leg_a_executing__traveling["leg_a_executing.traveling"]
  leg_b_awaiting_reply["leg_b_awaiting_reply"]
  leg_b_error["leg_b_error"]
  leg_b_executing["leg_b_executing"]
  leg_b_executing__event_breakdown["leg_b_executing.event_breakdown"]
  leg_b_executing__event_disease["leg_b_executing.event_disease"]
  leg_b_executing__event_encounter["leg_b_executing.event_encounter"]
  leg_b_executing__event_supply_loss["leg_b_executing.event_supply_loss"]
  leg_b_executing__event_weather["leg_b_executing.event_weather"]
  leg_b_executing__traveling["leg_b_executing.traveling"]
  leg_c_awaiting_reply["leg_c_awaiting_reply"]
  leg_c_error["leg_c_error"]
  leg_c_executing["leg_c_executing"]
  leg_c_executing__event_breakdown["leg_c_executing.event_breakdown"]
  leg_c_executing__event_disease["leg_c_executing.event_disease"]
  leg_c_executing__event_encounter["leg_c_executing.event_encounter"]
  leg_c_executing__event_supply_loss["leg_c_executing.event_supply_loss"]
  leg_c_executing__event_weather["leg_c_executing.event_weather"]
  leg_c_executing__traveling["leg_c_executing.traveling"]
  leg_d_awaiting_reply["leg_d_awaiting_reply"]
  leg_d_error["leg_d_error"]
  leg_d_executing["leg_d_executing"]
  leg_d_executing__event_breakdown["leg_d_executing.event_breakdown"]
  leg_d_executing__event_disease["leg_d_executing.event_disease"]
  leg_d_executing__event_encounter["leg_d_executing.event_encounter"]
  leg_d_executing__event_supply_loss["leg_d_executing.event_supply_loss"]
  leg_d_executing__event_weather["leg_d_executing.event_weather"]
  leg_d_executing__traveling["leg_d_executing.traveling"]
  leg_e_awaiting_reply["leg_e_awaiting_reply"]
  leg_e_error["leg_e_error"]
  leg_e_executing["leg_e_executing"]
  leg_e_executing__event_breakdown["leg_e_executing.event_breakdown"]
  leg_e_executing__event_disease["leg_e_executing.event_disease"]
  leg_e_executing__event_encounter["leg_e_executing.event_encounter"]
  leg_e_executing__event_supply_loss["leg_e_executing.event_supply_loss"]
  leg_e_executing__event_weather["leg_e_executing.event_weather"]
  leg_e_executing__traveling["leg_e_executing.traveling"]
  leg_f_awaiting_reply["leg_f_awaiting_reply"]
  leg_f_error["leg_f_error"]
  leg_f_executing["leg_f_executing"]
  leg_f_executing__event_breakdown["leg_f_executing.event_breakdown"]
  leg_f_executing__event_disease["leg_f_executing.event_disease"]
  leg_f_executing__event_encounter["leg_f_executing.event_encounter"]
  leg_f_executing__event_supply_loss["leg_f_executing.event_supply_loss"]
  leg_f_executing__event_weather["leg_f_executing.event_weather"]
  leg_f_executing__traveling["leg_f_executing.traveling"]
  leg_g_awaiting_reply["leg_g_awaiting_reply"]
  leg_g_error["leg_g_error"]
  leg_g_executing["leg_g_executing"]
  leg_g_executing__event_breakdown["leg_g_executing.event_breakdown"]
  leg_g_executing__event_disease["leg_g_executing.event_disease"]
  leg_g_executing__event_encounter["leg_g_executing.event_encounter"]
  leg_g_executing__event_supply_loss["leg_g_executing.event_supply_loss"]
  leg_g_executing__event_weather["leg_g_executing.event_weather"]
  leg_g_executing__traveling["leg_g_executing.traveling"]
  rest_room["rest_room"]
  rest_room__rest_done["rest_room.rest_done"]
  rest_room__rest_idle["rest_room.rest_idle"]
  rest_room__rest_running["rest_room.rest_running"]
  river_crossing["river_crossing"]
  river_crossing__deep["river_crossing.deep"]
  river_crossing__executing["river_crossing.executing"]
  river_crossing__mid["river_crossing.mid"]
  river_crossing__reviewing["river_crossing.reviewing"]
  river_crossing__shallow["river_crossing.shallow"]
  robbery_aftermath["robbery_aftermath"]
  snow_blocked["snow_blocked"]
  trail_guide["trail_guide"]
  trail_guide__trail_guide_active["trail_guide.trail_guide_active"]
  trail_guide__trail_guide_active_new["trail_guide.trail_guide_active_new"]
  trail_guide__trail_guide_list["trail_guide.trail_guide_list"]
  world_clock["world_clock"]
  world_clock__calendar["world_clock.calendar"]
  world_clock__calendar__day_active["world_clock.calendar.day_active"]
  world_clock__weather["world_clock.weather"]
  world_clock__weather__dry["world_clock.weather.dry"]
  world_clock__weather__rain["world_clock.weather.rain"]
  world_clock__weather__snow["world_clock.weather.snow"]
  fort__compose -->|back| fort__idle
  fort__compose -->|look| fort__compose
  fort__compose -->|propose_kit [world.money >= int((int(slots.oxen ?? 0) * 40 + int(slots.food ?? 0) * 0.2 + int(slots.bullets ?? 0) * 2 + int(slots.clothing ?? 0) * 10 + int(slots.wheels ?? 0) * 10 + int(slots.axles ?? 0) * 10 + int(slots.tongues ?? 0) * 10) * world.local_price_pct / 100)]| fort__reviewing
  fort__compose -->|propose_kit (default)| fort__compose
  fort__describe -->|back| fort__idle
  fort__describe -->|look| fort__describe
  fort__describe -->|open_compose| fort__compose
  fort__describe -->|propose_budget [int(slots.total_cost) >= 1 && world.money >= int(slots.total_cost)]| fort__reviewing
  fort__describe -->|propose_budget (default)| fort__describe
  fort__done -->|leave_fort [world.current_landmark == 'Fort Kearney']| leg_c_executing
  fort__done -->|leave_fort [world.current_landmark == 'Fort Laramie']| leg_e_executing
  fort__done -->|leave_fort (default)| ended_lost
  fort__done -->|look| fort__done
  fort__done -->|repeat_purchase| fort__idle
  fort__idle -->|browse_items| fort__describe
  fort__idle -->|leave_fort [world.current_landmark == 'Fort Kearney']| leg_c_executing
  fort__idle -->|leave_fort [world.current_landmark == 'Fort Laramie']| leg_e_executing
  fort__idle -->|leave_fort (default)| ended_lost
  fort__idle -->|look| fort__idle
  fort__idle -->|open_compose| fort__compose
  fort__idle -->|propose_budget [int(slots.total_cost) >= 1 && world.money >= int(slots.total_cost)]| fort__reviewing
  fort__idle -->|propose_budget (default)| fort__idle
  fort__idle -->|propose_purchase [int(slots.total_cost) < 5 && world.money >= int(slots.total_cost)]| fort__done
  fort__idle -->|propose_purchase [world.money >= int(slots.total_cost)]| fort__reviewing
  fort__idle -->|propose_purchase (default)| fort__idle
  fort__reviewing -->|accept_purchase| fort__done
  fort__reviewing -->|cancel_purchase| fort__idle
  fort__reviewing -->|look| fort__reviewing
  fort__reviewing -->|refine_purchase| fort__reviewing
  frontier__bandits__encounter -->|frontier__bandits__fight [world.frontier__bandits__party_alive >= 2 + world.frontier__bandits__threat_level]| robbery_aftermath
  frontier__bandits__encounter -->|frontier__bandits__fight (default)| robbery_aftermath
  frontier__bandits__encounter -->|frontier__bandits__flee| robbery_aftermath
  frontier__bandits__encounter -->|frontier__bandits__look| frontier__bandits__encounter
  frontier__bandits__encounter -->|frontier__bandits__pay [world.frontier__bandits__party_money >= world.frontier__bandits__threat_level * 50]| robbery_aftermath
  frontier__bandits__encounter -->|frontier__bandits__pay (default)| frontier__bandits__encounter
  frontier__scouting -->|frontier__look| frontier__scouting
  frontier__scouting -->|frontier__proceed| frontier__bandits
  frontier__scouting -->|frontier__scout| frontier__scouting
  general_store__compose -->|back| general_store__idle
  general_store__compose -->|look| general_store__compose
  general_store__compose -->|propose_kit [world.money >= int((int(slots.oxen ?? 0) * 40 + int(slots.food ?? 0) * 0.2 + int(slots.bullets ?? 0) * 2 + int(slots.clothing ?? 0) * 10 + int(slots.wheels ?? 0) * 10 + int(slots.axles ?? 0) * 10 + int(slots.tongues ?? 0) * 10) * world.local_price_pct / 100)]| general_store__reviewing
  general_store__compose -->|propose_kit (default)| general_store__compose
  general_store__describe -->|back| general_store__idle
  general_store__describe -->|look| general_store__describe
  general_store__describe -->|open_compose| general_store__compose
  general_store__describe -->|propose_budget [int(slots.total_cost) >= 1 && world.money >= int(slots.total_cost)]| general_store__reviewing
  general_store__describe -->|propose_budget (default)| general_store__describe
  general_store__done -->|leave_store [world.oxen >= 2 && world.food_lbs >= 200]| leg_a_executing
  general_store__done -->|leave_store (default)| general_store__idle
  general_store__done -->|look| general_store__done
  general_store__done -->|repeat_purchase| general_store__idle
  general_store__idle -->|browse_items| general_store__describe
  general_store__idle -->|leave_store [world.oxen >= 2 && world.food_lbs >= 200]| leg_a_executing
  general_store__idle -->|leave_store (default)| general_store__idle
  general_store__idle -->|look| general_store__idle
  general_store__idle -->|open_compose| general_store__compose
  general_store__idle -->|propose_budget [int(slots.total_cost) >= 1 && world.money >= int(slots.total_cost)]| general_store__reviewing
  general_store__idle -->|propose_budget (default)| general_store__idle
  general_store__idle -->|propose_purchase [int(slots.total_cost) < 5 && world.money >= int(slots.total_cost)]| general_store__done
  general_store__idle -->|propose_purchase [world.money >= int(slots.total_cost)]| general_store__reviewing
  general_store__idle -->|propose_purchase (default)| general_store__idle
  general_store__reviewing -->|accept_purchase| general_store__done
  general_store__reviewing -->|cancel_purchase| general_store__idle
  general_store__reviewing -->|look| general_store__reviewing
  general_store__reviewing -->|refine_purchase| general_store__reviewing
  hunt__hunt_done -->|continue [world.current_landmark == 'Kansas River Crossing']| leg_a_awaiting_reply
  hunt__hunt_done -->|continue [world.current_landmark == 'Fort Kearney']| leg_b_awaiting_reply
  hunt__hunt_done -->|continue [world.current_landmark == 'Chimney Rock']| leg_c_awaiting_reply
  hunt__hunt_done -->|continue [world.current_landmark == 'Fort Laramie']| leg_d_awaiting_reply
  hunt__hunt_done -->|continue [world.current_landmark == 'South Pass']| leg_e_awaiting_reply
  hunt__hunt_done -->|continue [world.current_landmark == 'Snake River Crossing']| leg_f_awaiting_reply
  hunt__hunt_done -->|continue [world.current_landmark == 'Willamette Valley']| leg_g_awaiting_reply
  hunt__hunt_done -->|continue (default)| leg_a_awaiting_reply
  hunt__hunt_done -->|look| hunt__hunt_done
  hunt__hunt_idle -->|check_inbox| inbox
  hunt__hunt_idle -->|look| hunt__hunt_idle
  hunt__hunt_idle -->|shoot [int(slots.bullets) > 0 && int(slots.bullets) <= world.bullets]| hunt__hunt_running
  hunt__hunt_idle -->|shoot (default)| hunt__hunt_idle
  hunt__hunt_running -->|answer_clarification| hunt__hunt_running
  hunt__hunt_running -->|check_inbox| inbox
  hunt__hunt_running -->|continue [world.last_hunt_outcome != '']| hunt__hunt_done
  hunt__hunt_running -->|continue (default)| hunt__hunt_running
  hunt__hunt_running -->|look| hunt__hunt_running
  inbox -->|look| inbox
  intro -->|begin_setup| intro_profession
  intro -->|look| intro
  intro_month -->|back| intro_profession
  intro_month -->|look| intro_month
  intro_month -->|pick_month| intro_party_names
  intro_party_names -->|back| intro_month
  intro_party_names -->|continue [world.party_names != '']| intro_summary
  intro_party_names -->|continue [world.party_member_1 != '']| intro_summary
  intro_party_names -->|continue (default)| intro_party_names
  intro_party_names -->|generate_names [world.narration]| intro_party_names
  intro_party_names -->|generate_names (default)| intro_party_names
  intro_party_names -->|look| intro_party_names
  intro_party_names -->|name_member [slots.index == 1]| intro_party_names
  intro_party_names -->|name_member [slots.index == 2]| intro_party_names
  intro_party_names -->|name_member [slots.index == 3]| intro_party_names
  intro_party_names -->|name_member [slots.index == 4]| intro_party_names
  intro_party_names -->|name_member [slots.index == 5]| intro_party_names
  intro_party_names -->|name_member (default)| intro_party_names
  intro_party_names -->|name_party| intro_party_names
  intro_profession -->|back| intro
  intro_profession -->|look| intro_profession
  intro_profession -->|pick_profession| intro_month
  intro_summary -->|edit_step [slots.step == 'profession']| intro_profession
  intro_summary -->|edit_step [slots.step == 'month']| intro_month
  intro_summary -->|edit_step [slots.step == 'names']| intro_party_names
  intro_summary -->|look| intro_summary
  intro_summary -->|start_journey [world.party_names != '' && world.profession != nil && world.month != nil]| general_store
  intro_summary -->|start_journey (default)| intro_summary
  leg_a_awaiting_reply -->|approach_river [true]| river_crossing
  leg_a_awaiting_reply -->|approach_river (default)| leg_a_awaiting_reply
  leg_a_awaiting_reply -->|consult_guide| trail_guide
  leg_a_awaiting_reply -->|continue ['Kansas River Crossing' == 'South Pass' && (world.month == 'october' // world.month == 'november' // world.month == 'december' // world.month == 'january' // world.month == 'february')]| snow_blocked
  leg_a_awaiting_reply -->|continue (default)| leg_b_executing
  leg_a_awaiting_reply -->|enter_fort [false]| fort
  leg_a_awaiting_reply -->|enter_fort (default)| leg_a_awaiting_reply
  leg_a_awaiting_reply -->|face_robbery| frontier
  leg_a_awaiting_reply -->|give_up_leg [world.cycle__leg_a__on_failure < 2]| leg_a_executing
  leg_a_awaiting_reply -->|give_up_leg (default)| leg_a_error
  leg_a_awaiting_reply -->|hunt| hunt
  leg_a_awaiting_reply -->|quit| ended_lost
  leg_a_awaiting_reply -->|rest| rest_room
  leg_a_awaiting_reply -->|restart_from [slots.stage == 'independence' // slots.stage == 'kansas']| leg_a_executing
  leg_a_awaiting_reply -->|restart_from [slots.stage == 'kearney']| leg_b_executing
  leg_a_awaiting_reply -->|restart_from [slots.stage == 'chimney']| leg_c_executing
  leg_a_awaiting_reply -->|restart_from [slots.stage == 'laramie']| leg_d_executing
  leg_a_awaiting_reply -->|restart_from [slots.stage == 'south_pass']| leg_e_executing
  leg_a_awaiting_reply -->|restart_from [slots.stage == 'snake']| leg_f_executing
  leg_a_awaiting_reply -->|restart_from (default)| leg_a_awaiting_reply
  leg_a_awaiting_reply -->|scout| frontier
  leg_a_error -->|quit| ended_lost
  leg_a_error -->|retry| leg_a_executing
  leg_a_executing -->|on_failure [world.cycle__leg_a__on_failure < 2]| leg_a_executing
  leg_a_executing -->|on_failure (default)| leg_a_error
  leg_a_executing__event_breakdown -->|look| leg_a_executing__event_breakdown
  leg_a_executing__event_breakdown -->|quit| ended_lost
  leg_a_executing__event_breakdown -->|repair [world.breakdown_part == 'wheel' && world.spare_wheels >= 1]| leg_a_executing__traveling
  leg_a_executing__event_breakdown -->|repair [world.breakdown_part == 'axle' && world.spare_axles >= 1]| leg_a_executing__traveling
  leg_a_executing__event_breakdown -->|repair [world.breakdown_part == 'tongue' && world.spare_tongues >= 1]| leg_a_executing__traveling
  leg_a_executing__event_breakdown -->|repair [world.current_event_attempts < 2]| leg_a_executing__event_breakdown
  leg_a_executing__event_breakdown -->|repair (default)| leg_a_executing__traveling
  leg_a_executing__event_breakdown -->|wait_out| leg_a_executing__traveling
  leg_a_executing__event_disease -->|look| leg_a_executing__event_disease
  leg_a_executing__event_disease -->|move_on| leg_a_executing__traveling
  leg_a_executing__event_disease -->|quit| ended_lost
  leg_a_executing__event_disease -->|treat [world.clothing_sets >= 1 && world.food_lbs >= 50 && world.current_event_attempts < 2]| leg_a_executing__traveling
  leg_a_executing__event_disease -->|treat [(world.clothing_sets < 1 // world.food_lbs < 50) && world.current_event_attempts < 2]| leg_a_executing__event_disease
  leg_a_executing__event_disease -->|treat (default)| leg_a_executing__traveling
  leg_a_executing__event_disease -->|wait_out [world.health_avg < 30]| leg_a_executing__traveling
  leg_a_executing__event_disease -->|wait_out (default)| leg_a_executing__traveling
  leg_a_executing__event_encounter -->|accept_trade [world.food_lbs >= 50]| leg_a_executing__traveling
  leg_a_executing__event_encounter -->|accept_trade (default)| leg_a_executing__event_encounter
  leg_a_executing__event_encounter -->|decline_trade| leg_a_executing__traveling
  leg_a_executing__event_encounter -->|look| leg_a_executing__event_encounter
  leg_a_executing__event_encounter -->|move_on| leg_a_executing__traveling
  leg_a_executing__event_encounter -->|quit| ended_lost
  leg_a_executing__event_supply_loss -->|look| leg_a_executing__event_supply_loss
  leg_a_executing__event_supply_loss -->|move_on| leg_a_executing__traveling
  leg_a_executing__event_supply_loss -->|quit| ended_lost
  leg_a_executing__event_weather -->|look| leg_a_executing__event_weather
  leg_a_executing__event_weather -->|push_on| leg_a_executing__traveling
  leg_a_executing__event_weather -->|quit| ended_lost
  leg_a_executing__event_weather -->|wait_out| leg_a_executing__traveling
  leg_a_executing__traveling -->|continue [world.miles_traveled + int((world.pace == 'grueling' ? 22 : (world.pace == 'strenuous' ? 18 : 14)) * (world.month == 'december' // world.month == 'january' // world.month == 'february' ? 70 : (world.month == 'november' ? 80 : (world.month == 'march' // world.month == 'october' ? 85 : (world.month == 'april' // world.month == 'september' ? 95 : 100)))) / 100) >= 102]| leg_a_awaiting_reply
  leg_a_executing__traveling -->|continue [world.food_lbs - (world.rations == 'bare_bones' ? 6 : (world.rations == 'meager' ? 8 : 10)) <= 0]| ended_lost
  leg_a_executing__traveling -->|continue [int(world.miles_traveled + world.rng_counter) % 100 >= 60 && int(world.miles_traveled + world.rng_counter) % 100 < 75]| leg_a_executing__event_disease
  leg_a_executing__traveling -->|continue [int(world.miles_traveled + world.rng_counter) % 100 >= 75 && int(world.miles_traveled + world.rng_counter) % 100 < 85]| leg_a_executing__event_breakdown
  leg_a_executing__traveling -->|continue [int(world.miles_traveled + world.rng_counter) % 100 >= 85 && int(world.miles_traveled + world.rng_counter) % 100 < 92]| leg_a_executing__event_weather
  leg_a_executing__traveling -->|continue [int(world.miles_traveled + world.rng_counter) % 100 >= 92 && int(world.miles_traveled + world.rng_counter) % 100 < 97]| leg_a_executing__event_encounter
  leg_a_executing__traveling -->|continue [int(world.miles_traveled + world.rng_counter) % 100 >= 97]| leg_a_executing__event_supply_loss
  leg_a_executing__traveling -->|continue (default)| leg_a_executing__traveling
  leg_a_executing__traveling -->|look| leg_a_executing__traveling
  leg_a_executing__traveling -->|quit| ended_lost
  leg_a_executing__traveling -->|set_pace| leg_a_executing__traveling
  leg_a_executing__traveling -->|set_rations| leg_a_executing__traveling
  leg_b_awaiting_reply -->|approach_river [false]| river_crossing
  leg_b_awaiting_reply -->|approach_river (default)| leg_b_awaiting_reply
  leg_b_awaiting_reply -->|consult_guide| trail_guide
  leg_b_awaiting_reply -->|continue ['Fort Kearney' == 'South Pass' && (world.month == 'october' // world.month == 'november' // world.month == 'december' // world.month == 'january' // world.month == 'february')]| snow_blocked
  leg_b_awaiting_reply -->|continue (default)| leg_c_executing
  leg_b_awaiting_reply -->|enter_fort [true]| fort
  leg_b_awaiting_reply -->|enter_fort (default)| leg_b_awaiting_reply
  leg_b_awaiting_reply -->|face_robbery| frontier
  leg_b_awaiting_reply -->|give_up_leg [world.cycle__leg_b__on_failure < 2]| leg_a_executing
  leg_b_awaiting_reply -->|give_up_leg (default)| leg_b_error
  leg_b_awaiting_reply -->|hunt| hunt
  leg_b_awaiting_reply -->|quit| ended_lost
  leg_b_awaiting_reply -->|rest| rest_room
  leg_b_awaiting_reply -->|restart_from [slots.stage == 'independence' // slots.stage == 'kansas']| leg_a_executing
  leg_b_awaiting_reply -->|restart_from [slots.stage == 'kearney']| leg_b_executing
  leg_b_awaiting_reply -->|restart_from [slots.stage == 'chimney']| leg_c_executing
  leg_b_awaiting_reply -->|restart_from [slots.stage == 'laramie']| leg_d_executing
  leg_b_awaiting_reply -->|restart_from [slots.stage == 'south_pass']| leg_e_executing
  leg_b_awaiting_reply -->|restart_from [slots.stage == 'snake']| leg_f_executing
  leg_b_awaiting_reply -->|restart_from (default)| leg_b_awaiting_reply
  leg_b_awaiting_reply -->|scout| frontier
  leg_b_error -->|quit| ended_lost
  leg_b_error -->|retry| leg_b_executing
  leg_b_executing -->|on_failure [world.cycle__leg_b__on_failure < 2]| leg_a_executing
  leg_b_executing -->|on_failure (default)| leg_b_error
  leg_b_executing__event_breakdown -->|look| leg_b_executing__event_breakdown
  leg_b_executing__event_breakdown -->|quit| ended_lost
  leg_b_executing__event_breakdown -->|repair [world.breakdown_part == 'wheel' && world.spare_wheels >= 1]| leg_b_executing__traveling
  leg_b_executing__event_breakdown -->|repair [world.breakdown_part == 'axle' && world.spare_axles >= 1]| leg_b_executing__traveling
  leg_b_executing__event_breakdown -->|repair [world.breakdown_part == 'tongue' && world.spare_tongues >= 1]| leg_b_executing__traveling
  leg_b_executing__event_breakdown -->|repair [world.current_event_attempts < 2]| leg_b_executing__event_breakdown
  leg_b_executing__event_breakdown -->|repair (default)| leg_b_executing__traveling
  leg_b_executing__event_breakdown -->|wait_out| leg_b_executing__traveling
  leg_b_executing__event_disease -->|look| leg_b_executing__event_disease
  leg_b_executing__event_disease -->|move_on| leg_b_executing__traveling
  leg_b_executing__event_disease -->|quit| ended_lost
  leg_b_executing__event_disease -->|treat [world.clothing_sets >= 1 && world.food_lbs >= 50 && world.current_event_attempts < 2]| leg_b_executing__traveling
  leg_b_executing__event_disease -->|treat [(world.clothing_sets < 1 // world.food_lbs < 50) && world.current_event_attempts < 2]| leg_b_executing__event_disease
  leg_b_executing__event_disease -->|treat (default)| leg_b_executing__traveling
  leg_b_executing__event_disease -->|wait_out [world.health_avg < 30]| leg_b_executing__traveling
  leg_b_executing__event_disease -->|wait_out (default)| leg_b_executing__traveling
  leg_b_executing__event_encounter -->|accept_trade [world.food_lbs >= 50]| leg_b_executing__traveling
  leg_b_executing__event_encounter -->|accept_trade (default)| leg_b_executing__event_encounter
  leg_b_executing__event_encounter -->|decline_trade| leg_b_executing__traveling
  leg_b_executing__event_encounter -->|look| leg_b_executing__event_encounter
  leg_b_executing__event_encounter -->|move_on| leg_b_executing__traveling
  leg_b_executing__event_encounter -->|quit| ended_lost
  leg_b_executing__event_supply_loss -->|look| leg_b_executing__event_supply_loss
  leg_b_executing__event_supply_loss -->|move_on| leg_b_executing__traveling
  leg_b_executing__event_supply_loss -->|quit| ended_lost
  leg_b_executing__event_weather -->|look| leg_b_executing__event_weather
  leg_b_executing__event_weather -->|push_on| leg_b_executing__traveling
  leg_b_executing__event_weather -->|quit| ended_lost
  leg_b_executing__event_weather -->|wait_out| leg_b_executing__traveling
  leg_b_executing__traveling -->|continue [world.miles_traveled + int((world.pace == 'grueling' ? 22 : (world.pace == 'strenuous' ? 18 : 14)) * (world.month == 'december' // world.month == 'january' // world.month == 'february' ? 70 : (world.month == 'november' ? 80 : (world.month == 'march' // world.month == 'october' ? 85 : (world.month == 'april' // world.month == 'september' ? 95 : 100)))) / 100) >= 202]| leg_b_awaiting_reply
  leg_b_executing__traveling -->|continue [world.food_lbs - (world.rations == 'bare_bones' ? 6 : (world.rations == 'meager' ? 8 : 10)) <= 0]| ended_lost
  leg_b_executing__traveling -->|continue [int(world.miles_traveled + world.rng_counter) % 100 >= 60 && int(world.miles_traveled + world.rng_counter) % 100 < 75]| leg_b_executing__event_disease
  leg_b_executing__traveling -->|continue [int(world.miles_traveled + world.rng_counter) % 100 >= 75 && int(world.miles_traveled + world.rng_counter) % 100 < 85]| leg_b_executing__event_breakdown
  leg_b_executing__traveling -->|continue [int(world.miles_traveled + world.rng_counter) % 100 >= 85 && int(world.miles_traveled + world.rng_counter) % 100 < 92]| leg_b_executing__event_weather
  leg_b_executing__traveling -->|continue [int(world.miles_traveled + world.rng_counter) % 100 >= 92 && int(world.miles_traveled + world.rng_counter) % 100 < 97]| leg_b_executing__event_encounter
  leg_b_executing__traveling -->|continue [int(world.miles_traveled + world.rng_counter) % 100 >= 97]| leg_b_executing__event_supply_loss
  leg_b_executing__traveling -->|continue (default)| leg_b_executing__traveling
  leg_b_executing__traveling -->|look| leg_b_executing__traveling
  leg_b_executing__traveling -->|quit| ended_lost
  leg_b_executing__traveling -->|set_pace| leg_b_executing__traveling
  leg_b_executing__traveling -->|set_rations| leg_b_executing__traveling
  leg_c_awaiting_reply -->|approach_river [false]| river_crossing
  leg_c_awaiting_reply -->|approach_river (default)| leg_c_awaiting_reply
  leg_c_awaiting_reply -->|consult_guide| trail_guide
  leg_c_awaiting_reply -->|continue ['Chimney Rock' == 'South Pass' && (world.month == 'october' // world.month == 'november' // world.month == 'december' // world.month == 'january' // world.month == 'february')]| snow_blocked
  leg_c_awaiting_reply -->|continue (default)| leg_d_executing
  leg_c_awaiting_reply -->|enter_fort [false]| fort
  leg_c_awaiting_reply -->|enter_fort (default)| leg_c_awaiting_reply
  leg_c_awaiting_reply -->|face_robbery| frontier
  leg_c_awaiting_reply -->|give_up_leg [world.cycle__leg_c__on_failure < 2]| leg_b_executing
  leg_c_awaiting_reply -->|give_up_leg (default)| leg_c_error
  leg_c_awaiting_reply -->|hunt| hunt
  leg_c_awaiting_reply -->|quit| ended_lost
  leg_c_awaiting_reply -->|rest| rest_room
  leg_c_awaiting_reply -->|restart_from [slots.stage == 'independence' // slots.stage == 'kansas']| leg_a_executing
  leg_c_awaiting_reply -->|restart_from [slots.stage == 'kearney']| leg_b_executing
  leg_c_awaiting_reply -->|restart_from [slots.stage == 'chimney']| leg_c_executing
  leg_c_awaiting_reply -->|restart_from [slots.stage == 'laramie']| leg_d_executing
  leg_c_awaiting_reply -->|restart_from [slots.stage == 'south_pass']| leg_e_executing
  leg_c_awaiting_reply -->|restart_from [slots.stage == 'snake']| leg_f_executing
  leg_c_awaiting_reply -->|restart_from (default)| leg_c_awaiting_reply
  leg_c_awaiting_reply -->|scout| frontier
  leg_c_error -->|quit| ended_lost
  leg_c_error -->|retry| leg_c_executing
  leg_c_executing -->|on_failure [world.cycle__leg_c__on_failure < 2]| leg_b_executing
  leg_c_executing -->|on_failure (default)| leg_c_error
  leg_c_executing__event_breakdown -->|look| leg_c_executing__event_breakdown
  leg_c_executing__event_breakdown -->|quit| ended_lost
  leg_c_executing__event_breakdown -->|repair [world.breakdown_part == 'wheel' && world.spare_wheels >= 1]| leg_c_executing__traveling
  leg_c_executing__event_breakdown -->|repair [world.breakdown_part == 'axle' && world.spare_axles >= 1]| leg_c_executing__traveling
  leg_c_executing__event_breakdown -->|repair [world.breakdown_part == 'tongue' && world.spare_tongues >= 1]| leg_c_executing__traveling
  leg_c_executing__event_breakdown -->|repair [world.current_event_attempts < 2]| leg_c_executing__event_breakdown
  leg_c_executing__event_breakdown -->|repair (default)| leg_c_executing__traveling
  leg_c_executing__event_breakdown -->|wait_out| leg_c_executing__traveling
  leg_c_executing__event_disease -->|look| leg_c_executing__event_disease
  leg_c_executing__event_disease -->|move_on| leg_c_executing__traveling
  leg_c_executing__event_disease -->|quit| ended_lost
  leg_c_executing__event_disease -->|treat [world.clothing_sets >= 1 && world.food_lbs >= 50 && world.current_event_attempts < 2]| leg_c_executing__traveling
  leg_c_executing__event_disease -->|treat [(world.clothing_sets < 1 // world.food_lbs < 50) && world.current_event_attempts < 2]| leg_c_executing__event_disease
  leg_c_executing__event_disease -->|treat (default)| leg_c_executing__traveling
  leg_c_executing__event_disease -->|wait_out [world.health_avg < 30]| leg_c_executing__traveling
  leg_c_executing__event_disease -->|wait_out (default)| leg_c_executing__traveling
  leg_c_executing__event_encounter -->|accept_trade [world.food_lbs >= 50]| leg_c_executing__traveling
  leg_c_executing__event_encounter -->|accept_trade (default)| leg_c_executing__event_encounter
  leg_c_executing__event_encounter -->|decline_trade| leg_c_executing__traveling
  leg_c_executing__event_encounter -->|look| leg_c_executing__event_encounter
  leg_c_executing__event_encounter -->|move_on| leg_c_executing__traveling
  leg_c_executing__event_encounter -->|quit| ended_lost
  leg_c_executing__event_supply_loss -->|look| leg_c_executing__event_supply_loss
  leg_c_executing__event_supply_loss -->|move_on| leg_c_executing__traveling
  leg_c_executing__event_supply_loss -->|quit| ended_lost
  leg_c_executing__event_weather -->|look| leg_c_executing__event_weather
  leg_c_executing__event_weather -->|push_on| leg_c_executing__traveling
  leg_c_executing__event_weather -->|quit| ended_lost
  leg_c_executing__event_weather -->|wait_out| leg_c_executing__traveling
  leg_c_executing__traveling -->|continue [world.miles_traveled + int((world.pace == 'grueling' ? 22 : (world.pace == 'strenuous' ? 18 : 14)) * (world.month == 'december' // world.month == 'january' // world.month == 'february' ? 70 : (world.month == 'november' ? 80 : (world.month == 'march' // world.month == 'october' ? 85 : (world.month == 'april' // world.month == 'september' ? 95 : 100)))) / 100) >= 250]| leg_c_awaiting_reply
  leg_c_executing__traveling -->|continue [world.food_lbs - (world.rations == 'bare_bones' ? 6 : (world.rations == 'meager' ? 8 : 10)) <= 0]| ended_lost
  leg_c_executing__traveling -->|continue [int(world.miles_traveled + world.rng_counter) % 100 >= 60 && int(world.miles_traveled + world.rng_counter) % 100 < 75]| leg_c_executing__event_disease
  leg_c_executing__traveling -->|continue [int(world.miles_traveled + world.rng_counter) % 100 >= 75 && int(world.miles_traveled + world.rng_counter) % 100 < 85]| leg_c_executing__event_breakdown
  leg_c_executing__traveling -->|continue [int(world.miles_traveled + world.rng_counter) % 100 >= 85 && int(world.miles_traveled + world.rng_counter) % 100 < 92]| leg_c_executing__event_weather
  leg_c_executing__traveling -->|continue [int(world.miles_traveled + world.rng_counter) % 100 >= 92 && int(world.miles_traveled + world.rng_counter) % 100 < 97]| leg_c_executing__event_encounter
  leg_c_executing__traveling -->|continue [int(world.miles_traveled + world.rng_counter) % 100 >= 97]| leg_c_executing__event_supply_loss
  leg_c_executing__traveling -->|continue (default)| leg_c_executing__traveling
  leg_c_executing__traveling -->|look| leg_c_executing__traveling
  leg_c_executing__traveling -->|quit| ended_lost
  leg_c_executing__traveling -->|set_pace| leg_c_executing__traveling
  leg_c_executing__traveling -->|set_rations| leg_c_executing__traveling
  leg_d_awaiting_reply -->|approach_river [false]| river_crossing
  leg_d_awaiting_reply -->|approach_river (default)| leg_d_awaiting_reply
  leg_d_awaiting_reply -->|consult_guide| trail_guide
  leg_d_awaiting_reply -->|continue ['Fort Laramie' == 'South Pass' && (world.month == 'october' // world.month == 'november' // world.month == 'december' // world.month == 'january' // world.month == 'february')]| snow_blocked
  leg_d_awaiting_reply -->|continue (default)| leg_e_executing
  leg_d_awaiting_reply -->|enter_fort [true]| fort
  leg_d_awaiting_reply -->|enter_fort (default)| leg_d_awaiting_reply
  leg_d_awaiting_reply -->|face_robbery| frontier
  leg_d_awaiting_reply -->|give_up_leg [world.cycle__leg_d__on_failure < 2]| leg_c_executing
  leg_d_awaiting_reply -->|give_up_leg (default)| leg_d_error
  leg_d_awaiting_reply -->|hunt| hunt
  leg_d_awaiting_reply -->|quit| ended_lost
  leg_d_awaiting_reply -->|rest| rest_room
  leg_d_awaiting_reply -->|restart_from [slots.stage == 'independence' // slots.stage == 'kansas']| leg_a_executing
  leg_d_awaiting_reply -->|restart_from [slots.stage == 'kearney']| leg_b_executing
  leg_d_awaiting_reply -->|restart_from [slots.stage == 'chimney']| leg_c_executing
  leg_d_awaiting_reply -->|restart_from [slots.stage == 'laramie']| leg_d_executing
  leg_d_awaiting_reply -->|restart_from [slots.stage == 'south_pass']| leg_e_executing
  leg_d_awaiting_reply -->|restart_from [slots.stage == 'snake']| leg_f_executing
  leg_d_awaiting_reply -->|restart_from (default)| leg_d_awaiting_reply
  leg_d_awaiting_reply -->|scout| frontier
  leg_d_error -->|quit| ended_lost
  leg_d_error -->|retry| leg_d_executing
  leg_d_executing -->|on_failure [world.cycle__leg_d__on_failure < 2]| leg_c_executing
  leg_d_executing -->|on_failure (default)| leg_d_error
  leg_d_executing__event_breakdown -->|look| leg_d_executing__event_breakdown
  leg_d_executing__event_breakdown -->|quit| ended_lost
  leg_d_executing__event_breakdown -->|repair [world.breakdown_part == 'wheel' && world.spare_wheels >= 1]| leg_d_executing__traveling
  leg_d_executing__event_breakdown -->|repair [world.breakdown_part == 'axle' && world.spare_axles >= 1]| leg_d_executing__traveling
  leg_d_executing__event_breakdown -->|repair [world.breakdown_part == 'tongue' && world.spare_tongues >= 1]| leg_d_executing__traveling
  leg_d_executing__event_breakdown -->|repair [world.current_event_attempts < 2]| leg_d_executing__event_breakdown
  leg_d_executing__event_breakdown -->|repair (default)| leg_d_executing__traveling
  leg_d_executing__event_breakdown -->|wait_out| leg_d_executing__traveling
  leg_d_executing__event_disease -->|look| leg_d_executing__event_disease
  leg_d_executing__event_disease -->|move_on| leg_d_executing__traveling
  leg_d_executing__event_disease -->|quit| ended_lost
  leg_d_executing__event_disease -->|treat [world.clothing_sets >= 1 && world.food_lbs >= 50 && world.current_event_attempts < 2]| leg_d_executing__traveling
  leg_d_executing__event_disease -->|treat [(world.clothing_sets < 1 // world.food_lbs < 50) && world.current_event_attempts < 2]| leg_d_executing__event_disease
  leg_d_executing__event_disease -->|treat (default)| leg_d_executing__traveling
  leg_d_executing__event_disease -->|wait_out [world.health_avg < 30]| leg_d_executing__traveling
  leg_d_executing__event_disease -->|wait_out (default)| leg_d_executing__traveling
  leg_d_executing__event_encounter -->|accept_trade [world.food_lbs >= 50]| leg_d_executing__traveling
  leg_d_executing__event_encounter -->|accept_trade (default)| leg_d_executing__event_encounter
  leg_d_executing__event_encounter -->|decline_trade| leg_d_executing__traveling
  leg_d_executing__event_encounter -->|look| leg_d_executing__event_encounter
  leg_d_executing__event_encounter -->|move_on| leg_d_executing__traveling
  leg_d_executing__event_encounter -->|quit| ended_lost
  leg_d_executing__event_supply_loss -->|look| leg_d_executing__event_supply_loss
  leg_d_executing__event_supply_loss -->|move_on| leg_d_executing__traveling
  leg_d_executing__event_supply_loss -->|quit| ended_lost
  leg_d_executing__event_weather -->|look| leg_d_executing__event_weather
  leg_d_executing__event_weather -->|push_on| leg_d_executing__traveling
  leg_d_executing__event_weather -->|quit| ended_lost
  leg_d_executing__event_weather -->|wait_out| leg_d_executing__traveling
  leg_d_executing__traveling -->|continue [world.miles_traveled + int((world.pace == 'grueling' ? 22 : (world.pace == 'strenuous' ? 18 : 14)) * (world.month == 'december' // world.month == 'january' // world.month == 'february' ? 70 : (world.month == 'november' ? 80 : (world.month == 'march' // world.month == 'october' ? 85 : (world.month == 'april' // world.month == 'september' ? 95 : 100)))) / 100) >= 86]| leg_d_awaiting_reply
  leg_d_executing__traveling -->|continue [world.food_lbs - (world.rations == 'bare_bones' ? 6 : (world.rations == 'meager' ? 8 : 10)) <= 0]| ended_lost
  leg_d_executing__traveling -->|continue [int(world.miles_traveled + world.rng_counter) % 100 >= 60 && int(world.miles_traveled + world.rng_counter) % 100 < 75]| leg_d_executing__event_disease
  leg_d_executing__traveling -->|continue [int(world.miles_traveled + world.rng_counter) % 100 >= 75 && int(world.miles_traveled + world.rng_counter) % 100 < 85]| leg_d_executing__event_breakdown
  leg_d_executing__traveling -->|continue [int(world.miles_traveled + world.rng_counter) % 100 >= 85 && int(world.miles_traveled + world.rng_counter) % 100 < 92]| leg_d_executing__event_weather
  leg_d_executing__traveling -->|continue [int(world.miles_traveled + world.rng_counter) % 100 >= 92 && int(world.miles_traveled + world.rng_counter) % 100 < 97]| leg_d_executing__event_encounter
  leg_d_executing__traveling -->|continue [int(world.miles_traveled + world.rng_counter) % 100 >= 97]| leg_d_executing__event_supply_loss
  leg_d_executing__traveling -->|continue (default)| leg_d_executing__traveling
  leg_d_executing__traveling -->|look| leg_d_executing__traveling
  leg_d_executing__traveling -->|quit| ended_lost
  leg_d_executing__traveling -->|set_pace| leg_d_executing__traveling
  leg_d_executing__traveling -->|set_rations| leg_d_executing__traveling
  leg_e_awaiting_reply -->|approach_river [false]| river_crossing
  leg_e_awaiting_reply -->|approach_river (default)| leg_e_awaiting_reply
  leg_e_awaiting_reply -->|consult_guide| trail_guide
  leg_e_awaiting_reply -->|continue ['South Pass' == 'South Pass' && (world.month == 'october' // world.month == 'november' // world.month == 'december' // world.month == 'january' // world.month == 'february')]| snow_blocked
  leg_e_awaiting_reply -->|continue (default)| leg_f_executing
  leg_e_awaiting_reply -->|enter_fort [false]| fort
  leg_e_awaiting_reply -->|enter_fort (default)| leg_e_awaiting_reply
  leg_e_awaiting_reply -->|face_robbery| frontier
  leg_e_awaiting_reply -->|give_up_leg [world.cycle__leg_e__on_failure < 2]| leg_d_executing
  leg_e_awaiting_reply -->|give_up_leg (default)| leg_e_error
  leg_e_awaiting_reply -->|hunt| hunt
  leg_e_awaiting_reply -->|quit| ended_lost
  leg_e_awaiting_reply -->|rest| rest_room
  leg_e_awaiting_reply -->|restart_from [slots.stage == 'independence' // slots.stage == 'kansas']| leg_a_executing
  leg_e_awaiting_reply -->|restart_from [slots.stage == 'kearney']| leg_b_executing
  leg_e_awaiting_reply -->|restart_from [slots.stage == 'chimney']| leg_c_executing
  leg_e_awaiting_reply -->|restart_from [slots.stage == 'laramie']| leg_d_executing
  leg_e_awaiting_reply -->|restart_from [slots.stage == 'south_pass']| leg_e_executing
  leg_e_awaiting_reply -->|restart_from [slots.stage == 'snake']| leg_f_executing
  leg_e_awaiting_reply -->|restart_from (default)| leg_e_awaiting_reply
  leg_e_awaiting_reply -->|scout| frontier
  leg_e_error -->|quit| ended_lost
  leg_e_error -->|retry| leg_e_executing
  leg_e_executing -->|on_failure [world.cycle__leg_e__on_failure < 2]| leg_d_executing
  leg_e_executing -->|on_failure (default)| leg_e_error
  leg_e_executing__event_breakdown -->|look| leg_e_executing__event_breakdown
  leg_e_executing__event_breakdown -->|quit| ended_lost
  leg_e_executing__event_breakdown -->|repair [world.breakdown_part == 'wheel' && world.spare_wheels >= 1]| leg_e_executing__traveling
  leg_e_executing__event_breakdown -->|repair [world.breakdown_part == 'axle' && world.spare_axles >= 1]| leg_e_executing__traveling
  leg_e_executing__event_breakdown -->|repair [world.breakdown_part == 'tongue' && world.spare_tongues >= 1]| leg_e_executing__traveling
  leg_e_executing__event_breakdown -->|repair [world.current_event_attempts < 2]| leg_e_executing__event_breakdown
  leg_e_executing__event_breakdown -->|repair (default)| leg_e_executing__traveling
  leg_e_executing__event_breakdown -->|wait_out| leg_e_executing__traveling
  leg_e_executing__event_disease -->|look| leg_e_executing__event_disease
  leg_e_executing__event_disease -->|move_on| leg_e_executing__traveling
  leg_e_executing__event_disease -->|quit| ended_lost
  leg_e_executing__event_disease -->|treat [world.clothing_sets >= 1 && world.food_lbs >= 50 && world.current_event_attempts < 2]| leg_e_executing__traveling
  leg_e_executing__event_disease -->|treat [(world.clothing_sets < 1 // world.food_lbs < 50) && world.current_event_attempts < 2]| leg_e_executing__event_disease
  leg_e_executing__event_disease -->|treat (default)| leg_e_executing__traveling
  leg_e_executing__event_disease -->|wait_out [world.health_avg < 30]| leg_e_executing__traveling
  leg_e_executing__event_disease -->|wait_out (default)| leg_e_executing__traveling
  leg_e_executing__event_encounter -->|accept_trade [world.food_lbs >= 50]| leg_e_executing__traveling
  leg_e_executing__event_encounter -->|accept_trade (default)| leg_e_executing__event_encounter
  leg_e_executing__event_encounter -->|decline_trade| leg_e_executing__traveling
  leg_e_executing__event_encounter -->|look| leg_e_executing__event_encounter
  leg_e_executing__event_encounter -->|move_on| leg_e_executing__traveling
  leg_e_executing__event_encounter -->|quit| ended_lost
  leg_e_executing__event_supply_loss -->|look| leg_e_executing__event_supply_loss
  leg_e_executing__event_supply_loss -->|move_on| leg_e_executing__traveling
  leg_e_executing__event_supply_loss -->|quit| ended_lost
  leg_e_executing__event_weather -->|look| leg_e_executing__event_weather
  leg_e_executing__event_weather -->|push_on| leg_e_executing__traveling
  leg_e_executing__event_weather -->|quit| ended_lost
  leg_e_executing__event_weather -->|wait_out| leg_e_executing__traveling
  leg_e_executing__traveling -->|continue [world.miles_traveled + int((world.pace == 'grueling' ? 22 : (world.pace == 'strenuous' ? 18 : 14)) * (world.month == 'december' // world.month == 'january' // world.month == 'february' ? 70 : (world.month == 'november' ? 80 : (world.month == 'march' // world.month == 'october' ? 85 : (world.month == 'april' // world.month == 'september' ? 95 : 100)))) / 100) >= 292]| leg_e_awaiting_reply
  leg_e_executing__traveling -->|continue [world.food_lbs - (world.rations == 'bare_bones' ? 6 : (world.rations == 'meager' ? 8 : 10)) <= 0]| ended_lost
  leg_e_executing__traveling -->|continue [int(world.miles_traveled + world.rng_counter) % 100 >= 60 && int(world.miles_traveled + world.rng_counter) % 100 < 75]| leg_e_executing__event_disease
  leg_e_executing__traveling -->|continue [int(world.miles_traveled + world.rng_counter) % 100 >= 75 && int(world.miles_traveled + world.rng_counter) % 100 < 85]| leg_e_executing__event_breakdown
  leg_e_executing__traveling -->|continue [int(world.miles_traveled + world.rng_counter) % 100 >= 85 && int(world.miles_traveled + world.rng_counter) % 100 < 92]| leg_e_executing__event_weather
  leg_e_executing__traveling -->|continue [int(world.miles_traveled + world.rng_counter) % 100 >= 92 && int(world.miles_traveled + world.rng_counter) % 100 < 97]| leg_e_executing__event_encounter
  leg_e_executing__traveling -->|continue [int(world.miles_traveled + world.rng_counter) % 100 >= 97]| leg_e_executing__event_supply_loss
  leg_e_executing__traveling -->|continue (default)| leg_e_executing__traveling
  leg_e_executing__traveling -->|look| leg_e_executing__traveling
  leg_e_executing__traveling -->|quit| ended_lost
  leg_e_executing__traveling -->|set_pace| leg_e_executing__traveling
  leg_e_executing__traveling -->|set_rations| leg_e_executing__traveling
  leg_f_awaiting_reply -->|approach_river [true]| river_crossing
  leg_f_awaiting_reply -->|approach_river (default)| leg_f_awaiting_reply
  leg_f_awaiting_reply -->|consult_guide| trail_guide
  leg_f_awaiting_reply -->|continue ['Snake River Crossing' == 'South Pass' && (world.month == 'october' // world.month == 'november' // world.month == 'december' // world.month == 'january' // world.month == 'february')]| snow_blocked
  leg_f_awaiting_reply -->|continue (default)| leg_g_executing
  leg_f_awaiting_reply -->|enter_fort [false]| fort
  leg_f_awaiting_reply -->|enter_fort (default)| leg_f_awaiting_reply
  leg_f_awaiting_reply -->|face_robbery| frontier
  leg_f_awaiting_reply -->|give_up_leg [world.cycle__leg_f__on_failure < 2]| leg_e_executing
  leg_f_awaiting_reply -->|give_up_leg (default)| leg_f_error
  leg_f_awaiting_reply -->|hunt| hunt
  leg_f_awaiting_reply -->|quit| ended_lost
  leg_f_awaiting_reply -->|rest| rest_room
  leg_f_awaiting_reply -->|restart_from [slots.stage == 'independence' // slots.stage == 'kansas']| leg_a_executing
  leg_f_awaiting_reply -->|restart_from [slots.stage == 'kearney']| leg_b_executing
  leg_f_awaiting_reply -->|restart_from [slots.stage == 'chimney']| leg_c_executing
  leg_f_awaiting_reply -->|restart_from [slots.stage == 'laramie']| leg_d_executing
  leg_f_awaiting_reply -->|restart_from [slots.stage == 'south_pass']| leg_e_executing
  leg_f_awaiting_reply -->|restart_from [slots.stage == 'snake']| leg_f_executing
  leg_f_awaiting_reply -->|restart_from (default)| leg_f_awaiting_reply
  leg_f_awaiting_reply -->|scout| frontier
  leg_f_error -->|quit| ended_lost
  leg_f_error -->|retry| leg_f_executing
  leg_f_executing -->|on_failure [world.cycle__leg_f__on_failure < 2]| leg_e_executing
  leg_f_executing -->|on_failure (default)| leg_f_error
  leg_f_executing__event_breakdown -->|look| leg_f_executing__event_breakdown
  leg_f_executing__event_breakdown -->|quit| ended_lost
  leg_f_executing__event_breakdown -->|repair [world.breakdown_part == 'wheel' && world.spare_wheels >= 1]| leg_f_executing__traveling
  leg_f_executing__event_breakdown -->|repair [world.breakdown_part == 'axle' && world.spare_axles >= 1]| leg_f_executing__traveling
  leg_f_executing__event_breakdown -->|repair [world.breakdown_part == 'tongue' && world.spare_tongues >= 1]| leg_f_executing__traveling
  leg_f_executing__event_breakdown -->|repair [world.current_event_attempts < 2]| leg_f_executing__event_breakdown
  leg_f_executing__event_breakdown -->|repair (default)| leg_f_executing__traveling
  leg_f_executing__event_breakdown -->|wait_out| leg_f_executing__traveling
  leg_f_executing__event_disease -->|look| leg_f_executing__event_disease
  leg_f_executing__event_disease -->|move_on| leg_f_executing__traveling
  leg_f_executing__event_disease -->|quit| ended_lost
  leg_f_executing__event_disease -->|treat [world.clothing_sets >= 1 && world.food_lbs >= 50 && world.current_event_attempts < 2]| leg_f_executing__traveling
  leg_f_executing__event_disease -->|treat [(world.clothing_sets < 1 // world.food_lbs < 50) && world.current_event_attempts < 2]| leg_f_executing__event_disease
  leg_f_executing__event_disease -->|treat (default)| leg_f_executing__traveling
  leg_f_executing__event_disease -->|wait_out [world.health_avg < 30]| leg_f_executing__traveling
  leg_f_executing__event_disease -->|wait_out (default)| leg_f_executing__traveling
  leg_f_executing__event_encounter -->|accept_trade [world.food_lbs >= 50]| leg_f_executing__traveling
  leg_f_executing__event_encounter -->|accept_trade (default)| leg_f_executing__event_encounter
  leg_f_executing__event_encounter -->|decline_trade| leg_f_executing__traveling
  leg_f_executing__event_encounter -->|look| leg_f_executing__event_encounter
  leg_f_executing__event_encounter -->|move_on| leg_f_executing__traveling
  leg_f_executing__event_encounter -->|quit| ended_lost
  leg_f_executing__event_supply_loss -->|look| leg_f_executing__event_supply_loss
  leg_f_executing__event_supply_loss -->|move_on| leg_f_executing__traveling
  leg_f_executing__event_supply_loss -->|quit| ended_lost
  leg_f_executing__event_weather -->|look| leg_f_executing__event_weather
  leg_f_executing__event_weather -->|push_on| leg_f_executing__traveling
  leg_f_executing__event_weather -->|quit| ended_lost
  leg_f_executing__event_weather -->|wait_out| leg_f_executing__traveling
  leg_f_executing__traveling -->|continue [world.miles_traveled + int((world.pace == 'grueling' ? 22 : (world.pace == 'strenuous' ? 18 : 14)) * (world.month == 'december' // world.month == 'january' // world.month == 'february' ? 70 : (world.month == 'november' ? 80 : (world.month == 'march' // world.month == 'october' ? 85 : (world.month == 'april' // world.month == 'september' ? 95 : 100)))) / 100) >= 250]| leg_f_awaiting_reply
  leg_f_executing__traveling -->|continue [world.food_lbs - (world.rations == 'bare_bones' ? 6 : (world.rations == 'meager' ? 8 : 10)) <= 0]| ended_lost
  leg_f_executing__traveling -->|continue [int(world.miles_traveled + world.rng_counter) % 100 >= 60 && int(world.miles_traveled + world.rng_counter) % 100 < 75]| leg_f_executing__event_disease
  leg_f_executing__traveling -->|continue [int(world.miles_traveled + world.rng_counter) % 100 >= 75 && int(world.miles_traveled + world.rng_counter) % 100 < 85]| leg_f_executing__event_breakdown
  leg_f_executing__traveling -->|continue [int(world.miles_traveled + world.rng_counter) % 100 >= 85 && int(world.miles_traveled + world.rng_counter) % 100 < 92]| leg_f_executing__event_weather
  leg_f_executing__traveling -->|continue [int(world.miles_traveled + world.rng_counter) % 100 >= 92 && int(world.miles_traveled + world.rng_counter) % 100 < 97]| leg_f_executing__event_encounter
  leg_f_executing__traveling -->|continue [int(world.miles_traveled + world.rng_counter) % 100 >= 97]| leg_f_executing__event_supply_loss
  leg_f_executing__traveling -->|continue (default)| leg_f_executing__traveling
  leg_f_executing__traveling -->|look| leg_f_executing__traveling
  leg_f_executing__traveling -->|quit| ended_lost
  leg_f_executing__traveling -->|set_pace| leg_f_executing__traveling
  leg_f_executing__traveling -->|set_rations| leg_f_executing__traveling
  leg_g_awaiting_reply -->|approach_river [false]| river_crossing
  leg_g_awaiting_reply -->|approach_river (default)| leg_g_awaiting_reply
  leg_g_awaiting_reply -->|consult_guide| trail_guide
  leg_g_awaiting_reply -->|continue ['Willamette Valley' == 'South Pass' && (world.month == 'october' // world.month == 'november' // world.month == 'december' // world.month == 'january' // world.month == 'february')]| snow_blocked
  leg_g_awaiting_reply -->|continue (default)| ended_won
  leg_g_awaiting_reply -->|enter_fort [false]| fort
  leg_g_awaiting_reply -->|enter_fort (default)| leg_g_awaiting_reply
  leg_g_awaiting_reply -->|face_robbery| frontier
  leg_g_awaiting_reply -->|give_up_leg [world.cycle__leg_g__on_failure < 2]| leg_f_executing
  leg_g_awaiting_reply -->|give_up_leg (default)| leg_g_error
  leg_g_awaiting_reply -->|hunt| hunt
  leg_g_awaiting_reply -->|quit| ended_lost
  leg_g_awaiting_reply -->|rest| rest_room
  leg_g_awaiting_reply -->|restart_from [slots.stage == 'independence' // slots.stage == 'kansas']| leg_a_executing
  leg_g_awaiting_reply -->|restart_from [slots.stage == 'kearney']| leg_b_executing
  leg_g_awaiting_reply -->|restart_from [slots.stage == 'chimney']| leg_c_executing
  leg_g_awaiting_reply -->|restart_from [slots.stage == 'laramie']| leg_d_executing
  leg_g_awaiting_reply -->|restart_from [slots.stage == 'south_pass']| leg_e_executing
  leg_g_awaiting_reply -->|restart_from [slots.stage == 'snake']| leg_f_executing
  leg_g_awaiting_reply -->|restart_from (default)| leg_g_awaiting_reply
  leg_g_awaiting_reply -->|scout| frontier
  leg_g_error -->|quit| ended_lost
  leg_g_error -->|retry| leg_g_executing
  leg_g_executing -->|on_failure [world.cycle__leg_g__on_failure < 2]| leg_f_executing
  leg_g_executing -->|on_failure (default)| leg_g_error
  leg_g_executing__event_breakdown -->|look| leg_g_executing__event_breakdown
  leg_g_executing__event_breakdown -->|quit| ended_lost
  leg_g_executing__event_breakdown -->|repair [world.breakdown_part == 'wheel' && world.spare_wheels >= 1]| leg_g_executing__traveling
  leg_g_executing__event_breakdown -->|repair [world.breakdown_part == 'axle' && world.spare_axles >= 1]| leg_g_executing__traveling
  leg_g_executing__event_breakdown -->|repair [world.breakdown_part == 'tongue' && world.spare_tongues >= 1]| leg_g_executing__traveling
  leg_g_executing__event_breakdown -->|repair [world.current_event_attempts < 2]| leg_g_executing__event_breakdown
  leg_g_executing__event_breakdown -->|repair (default)| leg_g_executing__traveling
  leg_g_executing__event_breakdown -->|wait_out| leg_g_executing__traveling
  leg_g_executing__event_disease -->|look| leg_g_executing__event_disease
  leg_g_executing__event_disease -->|move_on| leg_g_executing__traveling
  leg_g_executing__event_disease -->|quit| ended_lost
  leg_g_executing__event_disease -->|treat [world.clothing_sets >= 1 && world.food_lbs >= 50 && world.current_event_attempts < 2]| leg_g_executing__traveling
  leg_g_executing__event_disease -->|treat [(world.clothing_sets < 1 // world.food_lbs < 50) && world.current_event_attempts < 2]| leg_g_executing__event_disease
  leg_g_executing__event_disease -->|treat (default)| leg_g_executing__traveling
  leg_g_executing__event_disease -->|wait_out [world.health_avg < 30]| leg_g_executing__traveling
  leg_g_executing__event_disease -->|wait_out (default)| leg_g_executing__traveling
  leg_g_executing__event_encounter -->|accept_trade [world.food_lbs >= 50]| leg_g_executing__traveling
  leg_g_executing__event_encounter -->|accept_trade (default)| leg_g_executing__event_encounter
  leg_g_executing__event_encounter -->|decline_trade| leg_g_executing__traveling
  leg_g_executing__event_encounter -->|look| leg_g_executing__event_encounter
  leg_g_executing__event_encounter -->|move_on| leg_g_executing__traveling
  leg_g_executing__event_encounter -->|quit| ended_lost
  leg_g_executing__event_supply_loss -->|look| leg_g_executing__event_supply_loss
  leg_g_executing__event_supply_loss -->|move_on| leg_g_executing__traveling
  leg_g_executing__event_supply_loss -->|quit| ended_lost
  leg_g_executing__event_weather -->|look| leg_g_executing__event_weather
  leg_g_executing__event_weather -->|push_on| leg_g_executing__traveling
  leg_g_executing__event_weather -->|quit| ended_lost
  leg_g_executing__event_weather -->|wait_out| leg_g_executing__traveling
  leg_g_executing__traveling -->|continue [world.miles_traveled + int((world.pace == 'grueling' ? 22 : (world.pace == 'strenuous' ? 18 : 14)) * (world.month == 'december' // world.month == 'january' // world.month == 'february' ? 70 : (world.month == 'november' ? 80 : (world.month == 'march' // world.month == 'october' ? 85 : (world.month == 'april' // world.month == 'september' ? 95 : 100)))) / 100) >= 318]| leg_g_awaiting_reply
  leg_g_executing__traveling -->|continue [world.food_lbs - (world.rations == 'bare_bones' ? 6 : (world.rations == 'meager' ? 8 : 10)) <= 0]| ended_lost
  leg_g_executing__traveling -->|continue [int(world.miles_traveled + world.rng_counter) % 100 >= 60 && int(world.miles_traveled + world.rng_counter) % 100 < 75]| leg_g_executing__event_disease
  leg_g_executing__traveling -->|continue [int(world.miles_traveled + world.rng_counter) % 100 >= 75 && int(world.miles_traveled + world.rng_counter) % 100 < 85]| leg_g_executing__event_breakdown
  leg_g_executing__traveling -->|continue [int(world.miles_traveled + world.rng_counter) % 100 >= 85 && int(world.miles_traveled + world.rng_counter) % 100 < 92]| leg_g_executing__event_weather
  leg_g_executing__traveling -->|continue [int(world.miles_traveled + world.rng_counter) % 100 >= 92 && int(world.miles_traveled + world.rng_counter) % 100 < 97]| leg_g_executing__event_encounter
  leg_g_executing__traveling -->|continue [int(world.miles_traveled + world.rng_counter) % 100 >= 97]| leg_g_executing__event_supply_loss
  leg_g_executing__traveling -->|continue (default)| leg_g_executing__traveling
  leg_g_executing__traveling -->|look| leg_g_executing__traveling
  leg_g_executing__traveling -->|quit| ended_lost
  leg_g_executing__traveling -->|set_pace| leg_g_executing__traveling
  leg_g_executing__traveling -->|set_rations| leg_g_executing__traveling
  rest_room__rest_done -->|continue [world.current_landmark == 'Kansas River Crossing']| leg_a_awaiting_reply
  rest_room__rest_done -->|continue [world.current_landmark == 'Fort Kearney']| leg_b_awaiting_reply
  rest_room__rest_done -->|continue [world.current_landmark == 'Chimney Rock']| leg_c_awaiting_reply
  rest_room__rest_done -->|continue [world.current_landmark == 'Fort Laramie']| leg_d_awaiting_reply
  rest_room__rest_done -->|continue [world.current_landmark == 'South Pass']| leg_e_awaiting_reply
  rest_room__rest_done -->|continue [world.current_landmark == 'Snake River Crossing']| leg_f_awaiting_reply
  rest_room__rest_done -->|continue [world.current_landmark == 'Willamette Valley']| leg_g_awaiting_reply
  rest_room__rest_done -->|continue (default)| leg_a_awaiting_reply
  rest_room__rest_done -->|look| rest_room__rest_done
  rest_room__rest_idle -->|look| rest_room__rest_idle
  rest_room__rest_idle -->|rest [int(slots.days) > 0]| rest_room__rest_running
  rest_room__rest_idle -->|rest (default)| rest_room__rest_idle
  rest_room__rest_running -->|continue| rest_room__rest_done
  rest_room__rest_running -->|look| rest_room__rest_running
  river_crossing__deep -->|cancel_crossing [world.current_landmark == 'Kansas River Crossing']| leg_a_awaiting_reply
  river_crossing__deep -->|cancel_crossing [world.current_landmark == 'Snake River Crossing']| leg_f_awaiting_reply
  river_crossing__deep -->|cancel_crossing (default)| ended_lost
  river_crossing__deep -->|look| river_crossing__deep
  river_crossing__deep -->|propose_crossing| river_crossing__reviewing
  river_crossing__executing -->|continue [world.river_outcome == 'crossed' && world.current_landmark == 'Kansas River Crossing']| leg_b_executing
  river_crossing__executing -->|continue [world.river_outcome == 'crossed' && world.current_landmark == 'Snake River Crossing']| leg_g_executing
  river_crossing__executing -->|continue [world.river_outcome == 'swept_supplies' && world.river_depth_ft < 3]| river_crossing__shallow
  river_crossing__executing -->|continue [world.river_outcome == 'swept_supplies' && world.river_depth_ft < 6]| river_crossing__mid
  river_crossing__executing -->|continue [world.river_outcome == 'swept_supplies']| river_crossing__deep
  river_crossing__executing -->|continue [world.river_outcome == 'drowned' && world.river_depth_ft < 3]| river_crossing__shallow
  river_crossing__executing -->|continue [world.river_outcome == 'drowned' && world.river_depth_ft < 6]| river_crossing__mid
  river_crossing__executing -->|continue [world.river_outcome == 'drowned']| river_crossing__deep
  river_crossing__executing -->|continue (default)| river_crossing__executing
  river_crossing__executing -->|look| river_crossing__executing
  river_crossing__mid -->|cancel_crossing [world.current_landmark == 'Kansas River Crossing']| leg_a_awaiting_reply
  river_crossing__mid -->|cancel_crossing [world.current_landmark == 'Snake River Crossing']| leg_f_awaiting_reply
  river_crossing__mid -->|cancel_crossing (default)| ended_lost
  river_crossing__mid -->|look| river_crossing__mid
  river_crossing__mid -->|propose_crossing| river_crossing__reviewing
  river_crossing__reviewing -->|accept_crossing| river_crossing__executing
  river_crossing__reviewing -->|cancel_crossing [world.current_landmark == 'Kansas River Crossing']| leg_a_awaiting_reply
  river_crossing__reviewing -->|cancel_crossing [world.current_landmark == 'Snake River Crossing']| leg_f_awaiting_reply
  river_crossing__reviewing -->|cancel_crossing (default)| ended_lost
  river_crossing__reviewing -->|look| river_crossing__reviewing
  river_crossing__reviewing -->|refine_crossing [world.river_depth_ft < 3]| river_crossing__shallow
  river_crossing__reviewing -->|refine_crossing [world.river_depth_ft < 6]| river_crossing__mid
  river_crossing__reviewing -->|refine_crossing (default)| river_crossing__deep
  river_crossing__shallow -->|cancel_crossing [world.current_landmark == 'Kansas River Crossing']| leg_a_awaiting_reply
  river_crossing__shallow -->|cancel_crossing [world.current_landmark == 'Snake River Crossing']| leg_f_awaiting_reply
  river_crossing__shallow -->|cancel_crossing (default)| ended_lost
  river_crossing__shallow -->|look| river_crossing__shallow
  river_crossing__shallow -->|propose_crossing| river_crossing__reviewing
  robbery_aftermath -->|continue [world.current_landmark == 'Kansas River Crossing']| leg_a_awaiting_reply
  robbery_aftermath -->|continue [world.current_landmark == 'Fort Kearney']| leg_b_awaiting_reply
  robbery_aftermath -->|continue [world.current_landmark == 'Chimney Rock']| leg_c_awaiting_reply
  robbery_aftermath -->|continue [world.current_landmark == 'Fort Laramie']| leg_d_awaiting_reply
  robbery_aftermath -->|continue [world.current_landmark == 'South Pass']| leg_e_awaiting_reply
  robbery_aftermath -->|continue [world.current_landmark == 'Snake River Crossing']| leg_f_awaiting_reply
  robbery_aftermath -->|continue [world.current_landmark == 'Willamette Valley']| leg_g_awaiting_reply
  robbery_aftermath -->|continue (default)| leg_a_awaiting_reply
  robbery_aftermath -->|look| robbery_aftermath
  snow_blocked -->|give_up| ended_lost
  snow_blocked -->|look| snow_blocked
  snow_blocked -->|wait_for_spring [world.food_lbs - 120 <= 0]| ended_lost
  snow_blocked -->|wait_for_spring [world.month == 'march']| leg_e_awaiting_reply
  snow_blocked -->|wait_for_spring (default)| snow_blocked
  trail_guide__trail_guide_active -->|ask_question| trail_guide__trail_guide_active
  trail_guide__trail_guide_active -->|back| trail_guide__trail_guide_list
  trail_guide__trail_guide_active -->|look| trail_guide__trail_guide_active
  trail_guide__trail_guide_active_new -->|ask_question| trail_guide__trail_guide_active
  trail_guide__trail_guide_active_new -->|back| trail_guide__trail_guide_list
  trail_guide__trail_guide_active_new -->|look| trail_guide__trail_guide_active_new
  trail_guide__trail_guide_list -->|archive_chat| trail_guide__trail_guide_list
  trail_guide__trail_guide_list -->|ask_question| trail_guide__trail_guide_active_new
  trail_guide__trail_guide_list -->|fork_chat| trail_guide__trail_guide_active
  trail_guide__trail_guide_list -->|look| trail_guide__trail_guide_list
  trail_guide__trail_guide_list -->|open_chat| trail_guide__trail_guide_active
  trail_guide__trail_guide_list -->|rename_chat| trail_guide__trail_guide_list
  world_clock__calendar -->|precip_heavy| world_clock__calendar__day_active
  world_clock__calendar -->|snow_starts| world_clock__calendar__day_active
  world_clock__weather__dry -->|weather_advance| world_clock__weather__rain
  world_clock__weather__rain -->|weather_advance| world_clock__weather__snow
  world_clock__weather__snow -->|weather_advance| world_clock__weather__dry
```

## World Variables

| Name | Type | Default | Values |
|---|---|---|---|
| `at_fort` | `bool` | `false` |  |
| `breakdown_part` | `string` | `` |  |
| `bullets` | `int` | `0` |  |
| `cloak_of_dust_on` | `bool` | `false` |  |
| `clothing_sets` | `int` | `0` |  |
| `crossing_confidence` | `int` | `0` |  |
| `crossing_method` | `enum` | `none` | `none`, `ford`, `caulk`, `ferry`, `wait` |
| `current_event_attempts` | `int` | `0` |  |
| `current_landmark` | `string` | `Independence` |  |
| `cycle__leg_a__on_failure` | `int` | `0` |  |
| `cycle__leg_b__on_failure` | `int` | `0` |  |
| `cycle__leg_c__on_failure` | `int` | `0` |  |
| `cycle__leg_d__on_failure` | `int` | `0` |  |
| `cycle__leg_e__on_failure` | `int` | `0` |  |
| `cycle__leg_f__on_failure` | `int` | `0` |  |
| `cycle__leg_g__on_failure` | `int` | `0` |  |
| `day` | `int` | `1` |  |
| `encounter_kind` | `string` | `` |  |
| `event_kind` | `string` | `` |  |
| `food_lbs` | `int` | `0` |  |
| `frontier__bandits__member_lost` | `bool` | `false` |  |
| `frontier__bandits__outcome` | `string` | `` |  |
| `frontier__bandits__paid_amount` | `int` | `0` |  |
| `frontier__bandits__party_alive` | `int` | `5` |  |
| `frontier__bandits__party_food_lbs` | `int` | `0` |  |
| `frontier__bandits__party_money` | `int` | `0` |  |
| `frontier__bandits__threat_level` | `int` | `2` |  |
| `frontier__encounter_kind` | `string` | `` |  |
| `frontier__member_lost` | `bool` | `false` |  |
| `frontier__paid_amount` | `int` | `0` |  |
| `frontier__party_alive` | `int` | `5` |  |
| `frontier__party_money` | `int` | `0` |  |
| `frontier__scout_finding` | `string` | `` |  |
| `frontier__scouting_intel` | `string` | `` |  |
| `frontier__threat_level` | `int` | `2` |  |
| `health_avg` | `int` | `100` |  |
| `illness_kind` | `string` | `` |  |
| `illness_member` | `string` | `` |  |
| `illness_severity` | `int` | `0` |  |
| `illness_treatment` | `string` | `` |  |
| `inbox_unread` | `int` | `0` |  |
| `last_event` | `string` | `` |  |
| `last_event_prose` | `string` | `` |  |
| `last_hunt_lbs` | `int` | `0` |  |
| `last_hunt_outcome` | `string` | `` |  |
| `last_hunt_target` | `string` | `` |  |
| `last_job_id` | `string` | `` |  |
| `last_job_originating_state` | `string` | `` |  |
| `last_landmark_prose` | `string` | `` |  |
| `local_price_pct` | `int` | `100` |  |
| `miles_traveled` | `int` | `0` |  |
| `money` | `int` | `1600` |  |
| `month` | `enum` |  | `march`, `april`, `may`, `june`, `july`, `august`, `september`, `october`, `november`, `december`, `january`, `february` |
| `narration` | `bool` | `false` |  |
| `oxen` | `int` | `0` |  |
| `pace` | `enum` | `steady` | `steady`, `strenuous`, `grueling` |
| `party_alive` | `int` | `5` |  |
| `party_member_1` | `string` | `` |  |
| `party_member_2` | `string` | `` |  |
| `party_member_3` | `string` | `` |  |
| `party_member_4` | `string` | `` |  |
| `party_member_5` | `string` | `` |  |
| `party_names` | `string` | `` |  |
| `party_names_list` | `list` | `[]` |  |
| `party_size` | `int` | `5` |  |
| `pending_bullet_spend` | `int` | `0` |  |
| `pending_rest_days` | `int` | `0` |  |
| `precip_observed` | `bool` | `false` |  |
| `profession` | `enum` |  | `banker`, `carpenter`, `farmer` |
| `proposal_axles` | `int` | `0` |  |
| `proposal_bullets` | `int` | `0` |  |
| `proposal_clothing` | `int` | `0` |  |
| `proposal_food` | `int` | `0` |  |
| `proposal_items` | `string` | `` |  |
| `proposal_oxen` | `int` | `0` |  |
| `proposal_refine_count` | `int` | `0` |  |
| `proposal_tongues` | `int` | `0` |  |
| `proposal_total_cost` | `int` | `0` |  |
| `proposal_wheels` | `int` | `0` |  |
| `rations` | `enum` | `filling` | `filling`, `meager`, `bare_bones` |
| `river_depth_ft` | `int` | `0` |  |
| `river_outcome` | `string` | `` |  |
| `river_width_ft` | `int` | `0` |  |
| `rng_counter` | `int` | `0` |  |
| `rng_last` | `int` | `0` |  |
| `rng_seed` | `int` | `0` |  |
| `snow_observed` | `bool` | `false` |  |
| `spare_axles` | `int` | `0` |  |
| `spare_tongues` | `int` | `0` |  |
| `spare_wheels` | `int` | `0` |  |
| `wagon_answer` | `string` | `` |  |
| `wagon_chat_count` | `int` | `0` |  |
| `wagon_chat_id` | `string` | `` |  |
| `wagon_chat_title` | `string` | `` |  |
| `wagon_chat_turns` | `int` | `0` |  |
| `wagon_chats_view` | `string` | `` |  |
| `wagon_question` | `string` | `` |  |
| `wagon_session_id` | `string` | `` |  |
| `weather_kind` | `string` | `` |  |
| `year` | `int` | `1848` |  |

## Intents

### <a id="intent-accept-crossing"></a> `accept_crossing` — Accept and cross

Confirm the river-crossing draft and launch the wagon.

- Priority **90**
- Examples: `accept`, `go`, `do it`, `cross`

### <a id="intent-accept-purchase"></a> `accept_purchase` — Accept the proposal

Confirm the current buy_supplies draft and load up the wagon.

- Priority **90**
- Examples: `accept`, `yes`, `buy it`, `confirm`

### <a id="intent-accept-trade"></a> `accept_trade` — Accept the trade

Agree to the trade offered by the encountered party.

- Priority **80**
- Examples: `accept`, `accept the trade`, `trade with them`, `yes deal`

### <a id="intent-answer-clarification"></a> `answer_clarification` — Answer a clarification request

Provide an answer to a mid-flight background-job question (e.g. which species to hunt).

- Priority **85**
- Examples: `target bison`, `answer with elk`, `shoot the deer`

**Slots**:

| Name | Type | Required | Default | Values | Description |
|---|---|---|---|---|---|
| `answer` | `string` | yes |  |  | The answer to submit. For hunt clarification, the target species. |
| `job_id` | `string` | yes |  |  | ID of the job awaiting clarification (see inbox notification). |

### <a id="intent-approach-river"></a> `approach_river` — Approach the river

Walk down to the riverbank and assess the crossing options.

- Priority **75**
- Examples: `approach the river`, `go to the river`, `scout the crossing`

### <a id="intent-archive-chat"></a> `archive_chat` — Archive a wagon-master chat

Hide a wagon-master chat from the list.

- Priority **70**
- Examples: `archive 1`, `remove chat 2`, `forget that conversation`

**Slots**:

| Name | Type | Required | Default | Values | Description |
|---|---|---|---|---|---|
| `chat_id` | `string` | yes |  |  | Chat to archive (list position or ULID). |

### <a id="intent-ask-question"></a> `ask_question` — Ask the wagon master a question

Send a question to the wagon master in a chat. From the list, starts a fresh chat; from an active chat, continues it.

- Priority **85**
- Examples: `what should I do at the next river`, `ask: should we push on or rest`, `any advice for South Pass`

**Slots**:

| Name | Type | Required | Default | Values | Description |
|---|---|---|---|---|---|
| `question` | `string` | yes |  |  | The free-form question for the wagon master. |

### <a id="intent-back"></a> `back` — Go back

Return to the previous room (stackless — used by trail_guide and inbox).

- Priority **25**
- Examples: `back`, `return`, `go back`

### <a id="intent-begin-setup"></a> `begin_setup` — Begin setup

Start the intro wizard from the welcome screen.

- Priority **80**
- Examples: `begin`, `start setup`, `let's go`

### <a id="intent-browse-items"></a> `browse_items` — Browse what's on the shelves

Read Matt's item-by-item descriptions before drafting a purchase.

- Priority **40**
- Examples: `browse`, `what's available`, `describe items`, `look around`

### <a id="intent-cancel-crossing"></a> `cancel_crossing` — Cancel the crossing

Walk back from the riverbank to the prior landmark.

- Priority **50**
- Examples: `cancel`, `back away`, `not yet`

### <a id="intent-cancel-purchase"></a> `cancel_purchase` — Cancel the proposal

Walk away from the current draft and stay at the counter.

- Priority **50**
- Examples: `cancel`, `never mind`, `drop it`

### <a id="intent-caulk"></a> `caulk` — Caulk the wagon

Seal the wagon and float it across. Free; vulnerable to currents.

- Priority **90**
- Examples: `caulk`, `caulk the wagon`, `float across`, `seal and float`

### <a id="intent-check-inbox"></a> `check_inbox` — Check the inbox

Open the notification inbox to see pending background-job results.

- Priority **40**
- Examples: `inbox`, `check inbox`, `notifications`, `messages`

### <a id="intent-consult-guide"></a> `consult_guide` — Consult the wagon master

Open a persistent chat with the trail guide for advice. Chats are scoped by profession.

- Priority **55**
- Examples: `consult guide`, `ask the wagon master`, `advice`, `talk to the guide`

### <a id="intent-continue"></a> `continue` — Continue on the trail

Press on toward the next landmark.

- Priority **90**
- Examples: `continue`, `go`, `press on`, `keep going`, `onward`

### <a id="intent-decline-trade"></a> `decline_trade` — Decline the trade

Refuse the trade and move on.

- Priority **80**
- Examples: `decline`, `no trade`, `refuse`, `no thanks`

### <a id="intent-edit-step"></a> `edit_step` — Edit a setup step

Jump back to an earlier intro-wizard step (profession, month, or names) from the summary screen.

- Priority **30**
- Examples: `change profession`, `edit month`, `rename party`

**Slots**:

| Name | Type | Required | Default | Values | Description |
|---|---|---|---|---|---|
| `step` | `enum` | yes |  | `profession`, `month`, `names` | Which wizard step to return to. |

### <a id="intent-enter-fort"></a> `enter_fort` — Enter the fort

Pull into the fort at this landmark to resupply (forts have marked-up prices).

- Priority **75**
- Examples: `enter the fort`, `go into the fort`, `resupply at the fort`

### <a id="intent-ferry"></a> `ferry` — Take the ferry

Pay a ferryman to carry the wagon across. Safe; costs cash; may have a queue.

- Priority **90**
- Examples: `take the ferry`, `use the ferry`, `pay the ferryman`, `ferry across`

### <a id="intent-ford"></a> `ford` — Ford the river

Drive the wagon directly through the river. Free; risky in deep water.

- Priority **90**
- Examples: `ford`, `ford the river`, `drive across`

### <a id="intent-fork-chat"></a> `fork_chat` — Fork a wagon-master chat

Branch a wagon-master chat into a what-if scenario, starting from its current state.

- Priority **70**
- Examples: `fork 1 "what if we ferry"`, `branch chat 2 "alternate plan"`

**Slots**:

| Name | Type | Required | Default | Values | Description |
|---|---|---|---|---|---|
| `chat_id` | `string` | yes |  |  | Chat to fork (list position or ULID). |
| `title` | `string` | yes |  |  | Title for the new (forked) chat. |

### <a id="intent-frontier--bandits--fight"></a> `frontier__bandits__fight` — frontier__bandits__fight

Fight the bandits.

- Priority **70**
- Examples: `fight`, `shoot them`

### <a id="intent-frontier--bandits--flee"></a> `frontier__bandits__flee` — frontier__bandits__flee

Try to outrun them.

- Priority **60**
- Examples: `flee`, `run`, `ride off`

### <a id="intent-frontier--bandits--look"></a> `frontier__bandits__look` — frontier__bandits__look

Look around — re-render the scene.

- Priority **10**
- Examples: `look`, `what now?`

### <a id="intent-frontier--bandits--pay"></a> `frontier__bandits__pay` — frontier__bandits__pay

Pay the bandits off.

- Priority **80**
- Examples: `pay`, `give them the money`

### <a id="intent-frontier--look"></a> `frontier__look` — Look around

Re-render the current location and status.

- Priority **20**
- Examples: `look`, `look around`, `where am I`, `status`

### <a id="intent-frontier--proceed"></a> `frontier__proceed` — frontier__proceed

Press forward into whatever the scout found.

- Priority **70**
- Examples: `proceed`, `press on`, `go`

### <a id="intent-frontier--scout"></a> `frontier__scout` — Send a scout up the trail

Send a scout ahead to look the trail over before the wagons press on.

- Priority **80**
- Examples: `scout`, `send a scout`, `send a rider up the trail`, `look the road over`, `what's ahead`

### <a id="intent-generate-names"></a> `generate_names` — Generate party names from a theme

Auto-fill all party member names based on a theme (movie, era, mythology, ...).

- Priority **40**
- Examples: `generate names Western`, `name them all Star Wars`, `Norse mythology names`, `give us Lord of the Rings names`

**Slots**:

| Name | Type | Required | Default | Values | Description |
|---|---|---|---|---|---|
| `theme` | `string` | yes |  |  | A short theme description (movie, era, mythology, etc.). |

### <a id="intent-give-up"></a> `give_up` — Turn back east

Abandon the crossing and head back east. The journey ends here.

- Priority **20**
- Examples: `give up`, `turn back`, `go home`, `abandon the pass`

### <a id="intent-give-up-leg"></a> `give_up_leg` — Give up on this leg

Retreat from this landmark back to the previous one (counts against the on_failure cycle budget).

- Priority **30**
- Examples: `give up`, `give up on this leg`, `retreat`, `fall back`

### <a id="intent-hunt"></a> `hunt` — Hunt for game

Send a hunting party out to bring back meat. Costs bullets; result depends on terrain and luck.

- Priority **70**
- Examples: `hunt`, `go hunting`, `shoot game`, `look for meat`

### <a id="intent-leave-fort"></a> `leave_fort` — Leave the fort

Walk out of the fort and back onto the trail.

- Priority **80**
- Examples: `leave the fort`, `leave`, `out`, `back to the trail`

### <a id="intent-leave-store"></a> `leave_store` — Leave the store

Finish outfitting and depart toward the trail.

- Priority **80**
- Examples: `leave`, `leave the store`, `done shopping`, `head out`, `depart`

### <a id="intent-look"></a> `look` — Look around

Re-render the current location and status.

- Priority **20**
- Examples: `look`, `look around`, `where am I`, `status`

### <a id="intent-move-on"></a> `move_on` — Move on

Push past the current event without resolving it.

- Priority **70**
- Examples: `move on`, `press on anyway`, `ignore and continue`, `keep going`

### <a id="intent-name-member"></a> `name_member` — Name a specific member

Set the Nth party member's name (1-indexed).

- Priority **45**
- Examples: `name member 3 Carol`, `the leader is Adam`, `rename party member 2 to Sarah`

**Slots**:

| Name | Type | Required | Default | Values | Description |
|---|---|---|---|---|---|
| `index` | `int` | yes |  |  | Position in the party, 1-indexed (1 = leader). |
| `name` | `string` | yes |  |  | The new name. |

### <a id="intent-name-party"></a> `name_party` — Name the party

Set every party member's name in one comma-separated list.

- Priority **100**
- Examples: `name the party Adam, Beth, Carol, Daniel, Edith`, `we're Adam, Beth, Carol, Daniel, Edith`, `John,Sarah,Tom,Mary,Anne`

**Slots**:

| Name | Type | Required | Default | Values | Description |
|---|---|---|---|---|---|
| `names` | `string` | yes |  |  | Comma-separated list of party member names, leader first. |

### <a id="intent-on-failure"></a> `on_failure` — Phase failure (internal)

Synthetic intent used by the cycle_budgets:on_failure arc on each leg's _executing state. Not user-facing.

- Priority **1**
- Hidden (not shown in default menu)

### <a id="intent-open-chat"></a> `open_chat` — Open a wagon-master chat

Resume an existing wagon-master chat by list position or chat ID.

- Priority **80**
- Examples: `open 1`, `open chat abc`, `select 2`

**Slots**:

| Name | Type | Required | Default | Values | Description |
|---|---|---|---|---|---|
| `chat_id` | `string` | yes |  |  | Chat list-position, ULID prefix, or full chat ULID. |

### <a id="intent-open-compose"></a> `open_compose` — Open the compose form

Enter the item-by-item compose form for the general store.

- Priority **35**
- Examples: `compose`, `compose form`, `item-by-item`

### <a id="intent-open-job"></a> `open_job` — Open a job notification

From inbox: teleport back to the room that launched the job.

- Priority **35**
- Examples: `open job`, `open the last hunt`

**Slots**:

| Name | Type | Required | Default | Values | Description |
|---|---|---|---|---|---|
| `job_id` | `string` | yes |  |  | The job ID to open (fixtures pass {{ world.last_job_id }}). |

### <a id="intent-pick-month"></a> `pick_month` — Pick a departure month

Choose when to leave Independence. Too early = mud; too late = snow at South Pass.

- Priority **90**
- Examples: `leave in march`, `depart april`, `may`

**Slots**:

| Name | Type | Required | Default | Values | Description |
|---|---|---|---|---|---|
| `month` | `enum` | yes |  | `march`, `april`, `may`, `june`, `july` | Departure month. |

### <a id="intent-pick-profession"></a> `pick_profession` — Pick a profession

Choose the wagon leader's profession. This affects starting cash and final-score multiplier.

- Priority **95**
- Examples: `I am a banker`, `we are farmers`, `carpenter`

**Slots**:

| Name | Type | Required | Default | Values | Description |
|---|---|---|---|---|---|
| `profession` | `enum` | yes |  | `banker`, `carpenter`, `farmer` | The leader's profession (banker = most cash, farmer = highest score multiplier). |

### <a id="intent-precip-heavy"></a> `precip_heavy` — Heavy precipitation (cross-region emit)

Synthetic emit fired by the weather region on entering rain. Calendar reacts.

- Priority **1**
- Hidden (not shown in default menu)

### <a id="intent-propose-budget"></a> `propose_budget` — Propose a purchase by total budget

Tell Matt how much you want to spend; he assembles a balanced kit.

- Priority **80**
- Examples: `spend $200`, `spend 120`, `spend two hundred dollars`

**Slots**:

| Name | Type | Required | Default | Values | Description |
|---|---|---|---|---|---|
| `total_cost` | `int` | yes |  |  | Total dollar budget. Matt picks the basket. |

### <a id="intent-propose-crossing"></a> `propose_crossing` — Draft a crossing strategy

Pick a method (ford/caulk/ferry/wait) and a confidence.

- Priority **100**
- Examples: `ford the river`, `propose caulking with high confidence`, `let's ferry`

**Slots**:

| Name | Type | Required | Default | Values | Description |
|---|---|---|---|---|---|
| `confidence` | `int` |  | `50` |  | Confidence 0-100 (narrated-mode hint; deterministic-mode ignores). |
| `method` | `enum` | yes |  | `ford`, `caulk`, `ferry`, `wait` | How to cross. |

### <a id="intent-propose-kit"></a> `propose_kit` — Compose a kit item-by-item

Compose a purchase from per-item counts only — no free-text description. Total cost is computed from the counts × local prices.

- Priority **70**
- Examples: `buy 4 oxen, 1500 lbs food, 50 bullets`

**Slots**:

| Name | Type | Required | Default | Values | Description |
|---|---|---|---|---|---|
| `axles` | `int` |  |  |  | Spare axles ($10 each). |
| `bullets` | `int` |  |  |  | Boxes of bullets ($2.00/box of 20). |
| `clothing` | `int` |  |  |  | Sets of clothing ($10/set). |
| `food` | `int` |  |  |  | Pounds of food ($0.20/lb). |
| `oxen` | `int` |  |  |  | Number of oxen ($40/yoke at the Independence store). |
| `tongues` | `int` |  |  |  | Spare tongues ($10 each). |
| `wheels` | `int` |  |  |  | Spare wheels ($10 each). |

### <a id="intent-propose-purchase"></a> `propose_purchase` — Draft a purchase

Sketch a basket of supplies and a total cost to put in front of Matt.

- Priority **100**
- Examples: `buy 2 oxen and 200 lbs of food`, `propose 6 oxen, 300 lbs food, 1 set clothing`, `I'd like to spend $120`

**Slots**:

| Name | Type | Required | Default | Values | Description |
|---|---|---|---|---|---|
| `axles` | `int` |  | `0` |  | Number of spare axles in the basket. |
| `bullets` | `int` |  | `0` |  | Number of bullets (rounds, not boxes) in the basket. |
| `clothing` | `int` |  | `0` |  | Number of clothing sets in the basket. |
| `food` | `int` |  | `0` |  | Pounds of food in the basket. |
| `items` | `string` | yes |  |  | A short freeform description of the basket (item names + counts). |
| `oxen` | `int` |  | `0` |  | Number of yokes of oxen in the basket. |
| `tongues` | `int` |  | `0` |  | Number of spare wagon tongues in the basket. |
| `total_cost` | `int` | yes |  |  | Total dollar cost of the basket (already accounting for the local price multiplier). |
| `wheels` | `int` |  | `0` |  | Number of spare wagon wheels in the basket. |

### <a id="intent-push-on"></a> `push_on` — Push on

Push through bad weather, accepting the toll on the party.

- Priority **70**
- Examples: `push on`, `push through`, `go on anyway`, `press through the storm`

### <a id="intent-quit"></a> `quit` — Quit the journey

Abandon the trip. The journey ends here.

- Priority **15**
- Examples: `quit`, `abandon`, `give up`, `end the trip`

### <a id="intent-refine-crossing"></a> `refine_crossing` — Refine the crossing draft

Pick a different method or confidence — re-enters drafting.

- Priority **80**
- Examples: `refine`, `wait — let's caulk instead`, `actually ferry`

### <a id="intent-refine-purchase"></a> `refine_purchase` — Refine the proposal

Tweak fields of the current draft. Any per-item slot you supply replaces that line in the existing basket; everything you omit stays. Self-stays in reviewing so you can read the new draft and accept or refine again.

- Priority **80**
- Examples: `refine`, `I want 7 oxen not 6`, `actually 250 lbs of food`, `add 50 bullets`, `make it 4 spare wheels and bump the cost to 400`

**Slots**:

| Name | Type | Required | Default | Values | Description |
|---|---|---|---|---|---|
| `axles` | `int` |  |  |  | Replacement spare-axle count. |
| `bullets` | `int` |  |  |  | Replacement bullet count. |
| `clothing` | `int` |  |  |  | Replacement clothing-set count. |
| `feedback` | `string` |  | `` |  | Optional free-form note (logged for diagnostics; narrated-mode redraft will use it as a prompt hint in the future). |
| `food` | `int` |  |  |  | Replacement food (lbs). |
| `items` | `string` |  |  |  | Replacement description of the basket (free-form prose). |
| `oxen` | `int` |  |  |  | Replacement oxen count. |
| `tongues` | `int` |  |  |  | Replacement spare-tongue count. |
| `total_cost` | `int` |  |  |  | Replacement total cost. |
| `wheels` | `int` |  |  |  | Replacement spare-wheel count. |

### <a id="intent-rename-chat"></a> `rename_chat` — Rename a wagon-master chat

Change the title of an existing wagon-master chat.

- Priority **75**
- Examples: `rename 1 "river strategy"`, `title chat 2 oregon-pass-plan`

**Slots**:

| Name | Type | Required | Default | Values | Description |
|---|---|---|---|---|---|
| `chat_id` | `string` | yes |  |  | Chat to rename (list position or ULID). |
| `title` | `string` | yes |  |  | New title for the chat. |

### <a id="intent-repair"></a> `repair` — Repair the wagon

Use a spare part to repair a wagon breakdown.

- Priority **90**
- Examples: `repair`, `fix the wagon`, `replace the axle`, `swap the wheel`

### <a id="intent-repeat-purchase"></a> `repeat_purchase` — Draft another purchase

From the post-purchase view, start a new buy_supplies draft.

- Priority **70**
- Examples: `buy again`, `another`, `more shopping`

### <a id="intent-rest"></a> `rest` — Rest the party

Make camp for a number of days to recover health.

- Priority **70**
- Examples: `rest`, `camp`, `rest 3 days`, `make camp for a week`

**Slots**:

| Name | Type | Required | Default | Values | Description |
|---|---|---|---|---|---|
| `days` | `int` |  | `1` |  | How many game-days to rest. Defaults to 1. |

### <a id="intent-restart-from"></a> `restart_from` — Restart from a landmark

Teleport back to a previously visited landmark, keeping current supplies and party state.

- Priority **50**
- Examples: `go back to Fort Kearney`, `restart from chimney`, `return to Fort Laramie`

**Slots**:

| Name | Type | Required | Default | Values | Description |
|---|---|---|---|---|---|
| `stage` | `enum` | yes |  | `independence`, `kansas`, `kearney`, `chimney`, `laramie`, `south_pass`, `snake` | Which landmark to teleport back to. |

### <a id="intent-retry"></a> `retry` — Retry

Try the failed leg again from its starting landmark.

- Priority **50**
- Examples: `retry`, `try again`, `go back and try`

### <a id="intent-scout"></a> `scout` — Send a scout up the trail

Send a scout ahead to look the trail over before the wagons press on.

- Priority **80**
- Examples: `scout`, `send a scout`, `send a rider up the trail`, `look the road over`, `what's ahead`

### <a id="intent-set-pace"></a> `set_pace` — Set the pace

Change the travelling pace. Faster wears out the party; slower eats more food per mile.

- Priority **75**
- Examples: `set pace to grueling`, `slow down to steady`, `pace strenuous`

**Slots**:

| Name | Type | Required | Default | Values | Description |
|---|---|---|---|---|---|
| `pace` | `enum` | yes |  | `steady`, `strenuous`, `grueling` | Travelling pace. |

### <a id="intent-set-rations"></a> `set_rations` — Set rations

Change how much the party eats per day.

- Priority **75**
- Examples: `set rations meager`, `ration filling`, `eat bare bones`

**Slots**:

| Name | Type | Required | Default | Values | Description |
|---|---|---|---|---|---|
| `rations` | `enum` | yes |  | `filling`, `meager`, `bare_bones` | Daily food rationing level. |

### <a id="intent-shoot"></a> `shoot` — Shoot

Fire a specific number of bullets at game during a hunt.

- Priority **65**
- Examples: `shoot 5 bullets`, `fire 3 shots`

**Slots**:

| Name | Type | Required | Default | Values | Description |
|---|---|---|---|---|---|
| `bullets` | `int` | yes |  |  | How many bullets to expend on this shot. |

### <a id="intent-snow-starts"></a> `snow_starts` — Snow starts (cross-region emit)

Synthetic emit fired by the weather region on entering snow.

- Priority **1**
- Hidden (not shown in default menu)

### <a id="intent-start-journey"></a> `start_journey` — Start the journey

Leave the intro and proceed to the general store at Independence.

- Priority **150**
- Examples: `start`, `let's go`, `begin`, `start the journey`, `depart`

### <a id="intent-treat"></a> `treat` — Treat the sick

Use clothing and food to treat an ill party member. Cycle-budgeted (§7).

- Priority **90**
- Examples: `treat`, `treat the sick`, `tend the patient`, `doctor the ill`

### <a id="intent-wait"></a> `wait` — Wait at the river

Camp on this bank and wait for water levels to drop.

- Priority **60**
- Examples: `wait`, `wait for water to drop`, `camp here`, `delay crossing`

### <a id="intent-wait-for-spring"></a> `wait_for_spring` — Wait out the winter

Make winter camp and wait until spring opens South Pass. Burns food while you wait.

- Priority **70**
- Examples: `wait for spring`, `wait out the winter`, `winter camp`, `wait it out`

### <a id="intent-wait-out"></a> `wait_out` — Wait it out

Sit tight until the current event passes (storm, illness, encounter).

- Priority **75**
- Examples: `wait it out`, `ride it out`, `wait`, `hold up`

### <a id="intent-weather-advance"></a> `weather_advance` — Advance the weather

Tick the weather region (dry → rain → snow → dry).

- Priority **5**
- Hidden (not shown in default menu)
- Examples: `advance weather`, `tick`, `next weather`

## Rooms

### <a id="room-ended-lost"></a> `ended_lost`  _(terminal)_

The journey ends short of Oregon.

**Shows world**: `day`, `party_alive`, `current_landmark`, `miles_traveled`

**On enter**:

1. invoke `host.transport.post` with `body = "Day {{ world.day }}, {{ world.year }}. The trail claims another wagon party.\n\n**Last known location:** {{ world.current_landmark }} ({{ world.miles_traveled }} mi into the leg).\n**Survivors at the end:** {{ world.party_alive }}.\n\nMay the trail remember them.\n"`, `phase_id = "ended_lost_{{ run.id }}"`, `thread = "{{ run.id }}"`, `title = "Obituary"`, `transport = "tui"`

### <a id="room-ended-won"></a> `ended_won`  _(terminal)_

The wagon party has reached the Willamette Valley.

**Shows world**: `day`, `party_alive`, `food_lbs`, `oxen`, `money`, `profession`

**On enter**:

1. invoke `host.transport.post` with `body = "Day {{ world.day }}, {{ world.year }}. We made it.\n\n**{{ world.party_alive }}** survivors rolled into the Willamette Valley\nafter a long road from Independence.\n\n- Food remaining: {{ world.food_lbs }} lbs\n- Oxen: {{ world.oxen }}\n- Cash: ${{ world.money }}\n- Profession: {{ world.profession }}\n\nThe journey is over.\n"`, `phase_id = "ended_won_{{ run.id }}"`, `thread = "{{ run.id }}"`, `title = "Arrived in Willamette"`, `transport = "tui"`

### <a id="room-fort"></a> `fort`  _(compound)_

Inside the fort ({{ world.current_landmark }}) — buy_supplies at marked-up prices.

**Initial child**: `idle`

**Shows world**: `current_landmark`, `money`, `food_lbs`, `oxen`, `bullets`, `clothing_sets`, `spare_wheels`, `spare_axles`, `spare_tongues`, `local_price_pct`, `proposal_items`, `proposal_total_cost`, `proposal_refine_count`

**On enter**:

1. set `local_price_pct = 150`

### <a id="room-fort-compose"></a> `fort.compose`

Compose a kit item-by-item at fort price (numbers only).

**Transitions**:

| # | Intent | Guard | → | Effects |
|---|---|---|---|---|
| 1 | [`back`](#intent-back) |  | `../idle` |  |
| 2 | [`look`](#intent-look) |  | `.` |  |
| 3 | [`propose_kit`](#intent-propose-kit) | `world.money >= int((int(slots.oxen ?? 0) * 40 + int(slots.food ?? 0) * 0.2 + int(slots.bullets ?? 0) * 2 + int(slots.clothing ?? 0) * 10 + int(slots.wheels ?? 0) * 10 + int(slots.axles ?? 0) * 10 + int(slots.tongues ?? 0) * 10) * world.local_price_pct / 100)` | `../reviewing` | set `proposal_axles = "{{ int(slots.axles ?? 0) }}"`, `proposal_bullets = "{{ int(slots.bullets ?? 0) }}"`, `proposal_clothing = "{{ int(slots.clothing ?? 0) }}"`, `proposal_food = "{{ int(slots.food ?? 0) }}"`, `proposal_items = "{{ (int(slots.oxen ?? 0) > 0 ? string(int(slots.oxen)) + \" oxen, \" : \"\") + (int(slots.food ?? 0) > 0 ? string(int(slots.food)) + \" lbs food, \" : \"\") + (int(slots.bullets ?? 0) > 0 ? string(int(slots.bullets)) + \" bullets, \" : \"\") + (int(slots.clothing ?? 0) > 0 ? string(int(slots.clothing)) + \" clothing, \" : \"\") + (int(slots.wheels ?? 0) > 0 ? string(int(slots.wheels)) + \" wheels, \" : \"\") + (int(slots.axles ?? 0) > 0 ? string(int(slots.axles)) + \" axles, \" : \"\") + (int(slots.tongues ?? 0) > 0 ? string(int(slots.tongues)) + \" tongues\" : \"\") }}"`, `proposal_oxen = "{{ int(slots.oxen ?? 0) }}"`, `proposal_refine_count = 0`, `proposal_tongues = "{{ int(slots.tongues ?? 0) }}"`, `proposal_total_cost = "{{ int((int(slots.oxen ?? 0) * 40 + int(slots.food ?? 0) * 0.2 + int(slots.bullets ?? 0) * 2 + int(slots.clothing ?? 0) * 10 + int(slots.wheels ?? 0) * 10 + int(slots.axles ?? 0) * 10 + int(slots.tongues ?? 0) * 10) * world.local_price_pct / 100) }}"`, `proposal_wheels = "{{ int(slots.wheels ?? 0) }}"` · say "The sutler tallies the slate. 'Fort price comes to ${{ world.proposal_total_cost }} — take it or leave it.'" |
| 4 | [`propose_kit`](#intent-propose-kit) | _default_ | `.` | _hint: Not enough cash for that kit at fort prices._ · say "The sutler looks back at you. 'Haven't the coin for that, traveller.'" |

### <a id="room-fort-describe"></a> `fort.describe`

The sutler walks you through what's on the shelves.

**Transitions**:

| # | Intent | Guard | → | Effects |
|---|---|---|---|---|
| 1 | [`back`](#intent-back) |  | `../idle` |  |
| 2 | [`look`](#intent-look) |  | `.` |  |
| 3 | [`open_compose`](#intent-open-compose) |  | `../compose` |  |
| 4 | [`propose_budget`](#intent-propose-budget) | `int(slots.total_cost) >= 1 && world.money >= int(slots.total_cost)` | `../reviewing` | set `proposal_axles = "{{ int(slots.total_cost) >= 300 ? 1 : 0 }}"`, `proposal_bullets = "{{ int(int(slots.total_cost) * 10 / 100 / 2 / (world.local_price_pct / 100)) }}"`, `proposal_clothing = 0`, `proposal_food = "{{ int(int(slots.total_cost) * 50 / 100 / 0.2 / (world.local_price_pct / 100)) }}"`, `proposal_items = "Sutler's balanced kit"`, `proposal_oxen = "{{ int(int(slots.total_cost) * 30 / 100 / 40 / (world.local_price_pct / 100)) }}"`, `proposal_refine_count = 0`, `proposal_tongues = 0`, `proposal_total_cost = "{{ int(slots.total_cost) }}"`, `proposal_wheels = "{{ int(slots.total_cost) >= 150 ? 1 : 0 }}"` · say "The sutler scratches on the slate. 'Balanced kit for ${{ slots.total_cost }} — proposed.'" |
| 5 | [`propose_budget`](#intent-propose-budget) | _default_ | `.` | _hint: Budget must be positive and within your cash._ · say "The sutler shakes his head — 'budget doesn't carry, traveller.'" |

### <a id="room-fort-done"></a> `fort.done`

Purchase complete at {{ world.current_landmark }}.

**Shows world**: `money`, `food_lbs`, `oxen`, `current_landmark`

**On enter**:

1. invoke `host.transport.post` with `body = "Bought {{ world.proposal_items }} from the sutler at {{ world.current_landmark }} for ${{ world.proposal_total_cost }}. ${{ world.money }} cash left, {{ world.food_lbs }} lbs food, {{ world.oxen }} oxen."`, `phase_id = "fort_done_{{ world.current_landmark }}_{{ world.proposal_items }}_{{ world.proposal_total_cost }}"`, `thread = "{{ run.id }}"`, `title = "Resupplied at {{ world.current_landmark }}"`, `transport = "tui"`

**Transitions**:

| # | Intent | Guard | → | Effects |
|---|---|---|---|---|
| 1 | [`leave_fort`](#intent-leave-fort) | `world.current_landmark == 'Fort Kearney'` | [`leg_c_executing`](#room-leg-c-executing) | set `proposal_axles = 0`, `proposal_bullets = 0`, `proposal_clothing = 0`, `proposal_food = 0`, `proposal_items = ""`, `proposal_oxen = 0`, `proposal_refine_count = 0`, `proposal_tongues = 0`, `proposal_total_cost = 0`, `proposal_wheels = 0` · say "Back on the trail, heading for Chimney Rock." |
| 2 | [`leave_fort`](#intent-leave-fort) | `world.current_landmark == 'Fort Laramie'` | [`leg_e_executing`](#room-leg-e-executing) | set `proposal_axles = 0`, `proposal_bullets = 0`, `proposal_clothing = 0`, `proposal_food = 0`, `proposal_items = ""`, `proposal_oxen = 0`, `proposal_refine_count = 0`, `proposal_tongues = 0`, `proposal_total_cost = 0`, `proposal_wheels = 0` · say "Back on the trail, heading for South Pass." |
| 3 | [`leave_fort`](#intent-leave-fort) | _default_ | [`ended_lost`](#room-ended-lost) | _hint: Lost track of which fort we're in — ending the run._ |
| 4 | [`look`](#intent-look) |  | `.` |  |
| 5 | [`repeat_purchase`](#intent-repeat-purchase) |  | `../idle` | set `proposal_axles = 0`, `proposal_bullets = 0`, `proposal_clothing = 0`, `proposal_food = 0`, `proposal_items = ""`, `proposal_oxen = 0`, `proposal_refine_count = 0`, `proposal_tongues = 0`, `proposal_total_cost = 0`, `proposal_wheels = 0` · say "The sutler raises an eyebrow, doesn't look up. 'Something else?'" |

### <a id="room-fort-idle"></a> `fort.idle`

At the fort sutler ({{ world.current_landmark }}) — pick how to draft a purchase, or leave.

**Transitions**:

| # | Intent | Guard | → | Effects |
|---|---|---|---|---|
| 1 | [`browse_items`](#intent-browse-items) |  | `../describe` |  |
| 2 | [`leave_fort`](#intent-leave-fort) | `world.current_landmark == 'Fort Kearney'` | [`leg_c_executing`](#room-leg-c-executing) | set `proposal_axles = 0`, `proposal_bullets = 0`, `proposal_clothing = 0`, `proposal_food = 0`, `proposal_items = ""`, `proposal_oxen = 0`, `proposal_refine_count = 0`, `proposal_tongues = 0`, `proposal_total_cost = 0`, `proposal_wheels = 0` · say "Back on the trail, heading for Chimney Rock." |
| 3 | [`leave_fort`](#intent-leave-fort) | `world.current_landmark == 'Fort Laramie'` | [`leg_e_executing`](#room-leg-e-executing) | set `proposal_axles = 0`, `proposal_bullets = 0`, `proposal_clothing = 0`, `proposal_food = 0`, `proposal_items = ""`, `proposal_oxen = 0`, `proposal_refine_count = 0`, `proposal_tongues = 0`, `proposal_total_cost = 0`, `proposal_wheels = 0` · say "Back on the trail, heading for South Pass." |
| 4 | [`leave_fort`](#intent-leave-fort) | _default_ | [`ended_lost`](#room-ended-lost) | _hint: Lost track of which fort we're in — ending the run._ |
| 5 | [`look`](#intent-look) |  | `.` |  |
| 6 | [`open_compose`](#intent-open-compose) |  | `../compose` |  |
| 7 | [`propose_budget`](#intent-propose-budget) | `int(slots.total_cost) >= 1 && world.money >= int(slots.total_cost)` | `../reviewing` | set `proposal_axles = "{{ int(slots.total_cost) >= 300 ? 1 : 0 }}"`, `proposal_bullets = "{{ int(int(slots.total_cost) * 10 / 100 / 2 / (world.local_price_pct / 100)) }}"`, `proposal_clothing = 0`, `proposal_food = "{{ int(int(slots.total_cost) * 50 / 100 / 0.2 / (world.local_price_pct / 100)) }}"`, `proposal_items = "Sutler's balanced kit"`, `proposal_oxen = "{{ int(int(slots.total_cost) * 30 / 100 / 40 / (world.local_price_pct / 100)) }}"`, `proposal_refine_count = 0`, `proposal_tongues = 0`, `proposal_total_cost = "{{ int(slots.total_cost) }}"`, `proposal_wheels = "{{ int(slots.total_cost) >= 150 ? 1 : 0 }}"` · say "The sutler scratches on the slate, slides it across. 'Balanced kit for ${{ slots.total_cost }} at fort price — proposed.'" |
| 8 | [`propose_budget`](#intent-propose-budget) | _default_ | `.` | _hint: Budget must be a positive integer and within your cash on hand._ · say "The sutler eyes the slate. 'That budget won't carry at fort prices, traveller.'" |
| 9 | [`propose_purchase`](#intent-propose-purchase) | `int(slots.total_cost) < 5 && world.money >= int(slots.total_cost)` | `../done` | set `bullets = "{{ world.bullets + int(slots.bullets ?? 0) }}"`, `clothing_sets = "{{ world.clothing_sets + int(slots.clothing ?? 0) }}"`, `food_lbs = "{{ world.food_lbs + int(slots.food ?? 0) }}"`, `money = "{{ world.money - int(slots.total_cost) }}"`, `oxen = "{{ world.oxen + int(slots.oxen ?? 0) }}"`, `proposal_axles = "{{ int(slots.axles ?? 0) }}"`, `proposal_bullets = "{{ int(slots.bullets ?? 0) }}"`, `proposal_clothing = "{{ int(slots.clothing ?? 0) }}"`, `proposal_food = "{{ int(slots.food ?? 0) }}"`, `proposal_items = "{{ slots.items }}"`, `proposal_oxen = "{{ int(slots.oxen ?? 0) }}"`, `proposal_refine_count = 0`, `proposal_tongues = "{{ int(slots.tongues ?? 0) }}"`, `proposal_total_cost = "{{ int(slots.total_cost) }}"`, `proposal_wheels = "{{ int(slots.wheels ?? 0) }}"`, `spare_axles = "{{ world.spare_axles + int(slots.axles ?? 0) }}"`, `spare_tongues = "{{ world.spare_tongues + int(slots.tongues ?? 0) }}"`, `spare_wheels = "{{ world.spare_wheels + int(slots.wheels ?? 0) }}"` · say "The sutler grunts, tosses a tin onto the counter — {{ slots.items }} — and palms the ${{ slots.total_cost }}." |
| 10 | [`propose_purchase`](#intent-propose-purchase) | `world.money >= int(slots.total_cost)` | `../reviewing` | set `proposal_axles = "{{ int(slots.axles ?? 0) }}"`, `proposal_bullets = "{{ int(slots.bullets ?? 0) }}"`, `proposal_clothing = "{{ int(slots.clothing ?? 0) }}"`, `proposal_food = "{{ int(slots.food ?? 0) }}"`, `proposal_items = "{{ slots.items }}"`, `proposal_oxen = "{{ int(slots.oxen ?? 0) }}"`, `proposal_refine_count = 0`, `proposal_tongues = "{{ int(slots.tongues ?? 0) }}"`, `proposal_total_cost = "{{ int(slots.total_cost) }}"`, `proposal_wheels = "{{ int(slots.wheels ?? 0) }}"` · say "The sutler chalks it on the board behind him. '{{ slots.items }}. Fort price — ${{ slots.total_cost }}. Take it or leave it.'" |
| 11 | [`propose_purchase`](#intent-propose-purchase) | _default_ | `.` | _hint: Not enough cash at fort prices._ · say "The sutler shrugs. 'You haven't got the coin, traveller.'" |

### <a id="room-fort-reviewing"></a> `fort.reviewing`

The sutler reads the order back.

**Shows world**: `proposal_items`, `proposal_total_cost`, `money`, `proposal_refine_count`, `current_landmark`

**Transitions**:

| # | Intent | Guard | → | Effects |
|---|---|---|---|---|
| 1 | [`accept_purchase`](#intent-accept-purchase) |  | `../done` | set `bullets = "{{ world.bullets + world.proposal_bullets }}"`, `clothing_sets = "{{ world.clothing_sets + world.proposal_clothing }}"`, `food_lbs = "{{ world.food_lbs + world.proposal_food }}"`, `money = "{{ world.money - world.proposal_total_cost }}"`, `oxen = "{{ world.oxen + world.proposal_oxen }}"`, `spare_axles = "{{ world.spare_axles + world.proposal_axles }}"`, `spare_tongues = "{{ world.spare_tongues + world.proposal_tongues }}"`, `spare_wheels = "{{ world.spare_wheels + world.proposal_wheels }}"` · say "The sutler swipes the coins off the counter and pushes the goods at you with the back of his hand. 'Done. {{ world.proposal_items }}. ${{ world.money }} left in your purse — and you'll need it before Oregon.'" |
| 2 | [`cancel_purchase`](#intent-cancel-purchase) |  | `../idle` | set `proposal_axles = 0`, `proposal_bullets = 0`, `proposal_clothing = 0`, `proposal_food = 0`, `proposal_items = ""`, `proposal_oxen = 0`, `proposal_refine_count = 0`, `proposal_tongues = 0`, `proposal_total_cost = 0`, `proposal_wheels = 0` · say "The sutler erases the board with a damp rag, scowls. 'Suit yourself.'" |
| 3 | [`look`](#intent-look) |  | `.` |  |
| 4 | [`refine_purchase`](#intent-refine-purchase) |  | `../reviewing` | set `proposal_axles = "{{ int(slots.axles    ?? world.proposal_axles) }}"`, `proposal_bullets = "{{ int(slots.bullets  ?? world.proposal_bullets) }}"`, `proposal_clothing = "{{ int(slots.clothing ?? world.proposal_clothing) }}"`, `proposal_food = "{{ int(slots.food     ?? world.proposal_food) }}"`, `proposal_items = "{{ slots.items ?? world.proposal_items }}"`, `proposal_oxen = "{{ int(slots.oxen     ?? world.proposal_oxen) }}"`, `proposal_refine_count = "{{ world.proposal_refine_count + 1 }}"`, `proposal_tongues = "{{ int(slots.tongues  ?? world.proposal_tongues) }}"`, `proposal_total_cost = "{{ int(slots.total_cost ?? world.proposal_total_cost) }}"`, `proposal_wheels = "{{ int(slots.wheels   ?? world.proposal_wheels) }}"` · say "The sutler rubs out a line of chalk and writes the new one. 'That's the order then. Same fort price.' (revision {{ world.proposal_refine_count }})" |

### <a id="room-frontier"></a> `frontier`  _(compound)_

**Initial child**: `scouting`

**On enter**:

1. set `frontier__party_alive = "{{ world.party_alive }}"`
2. set `frontier__party_money = "{{ world.money }}"`
3. set `frontier__scouting_intel = "{{ world.current_landmark }} country — open prairie ahead."`
4. set `frontier__threat_level = "{{ world.miles_traveled < 500 ? 1 : (world.miles_traveled < 1200 ? 2 : 3) }}"`

### <a id="room-frontier-bandits"></a> `frontier.bandits`  _(compound)_

**Initial child**: `encounter`

**On enter**:

1. set `frontier__bandits__party_alive = "{{ world.frontier__party_alive }}"`
2. set `frontier__bandits__party_food_lbs = "0"`
3. set `frontier__bandits__party_money = "{{ world.frontier__party_money }}"`
4. set `frontier__bandits__threat_level = "{{ world.frontier__threat_level }}"`

### <a id="room-frontier-bandits-encounter"></a> `frontier.bandits.encounter`

A masked rider blocks the trail.

**Shows world**: `frontier__bandits__party_money`, `frontier__bandits__party_alive`, `frontier__bandits__threat_level`

**On enter**:

1. invoke `host.run.announce` with `cmd = "true # bandit shows up, threat={{ world.frontier__bandits__threat_level }}"`, `prompt = "/home/cloud-user/code/kitsoki/.worktrees/agent-split/stories/robbery/prompts/encounter_intro.md"`

**Transitions**:

| # | Intent | Guard | → | Effects |
|---|---|---|---|---|
| 1 | [`frontier__bandits__fight`](#intent-frontier--bandits--fight) | `world.frontier__bandits__party_alive >= 2 + world.frontier__bandits__threat_level` | [`robbery_aftermath`](#room-robbery-aftermath) | set `frontier__bandits__outcome = "routed"` · invoke `host.run.close` with `cmd = "true # routed"` · set `frontier__encounter_kind = "routed"` · set `last_event = "bandit_resolved"`, `money = "{{ world.money - world.frontier__paid_amount }}"` |
| 2 | [`frontier__bandits__fight`](#intent-frontier--bandits--fight) | _default_ | [`robbery_aftermath`](#room-robbery-aftermath) | set `frontier__bandits__member_lost = true`, `frontier__bandits__outcome = "killed"` · invoke `host.run.close` with `cmd = "true # killed"` · set `frontier__encounter_kind = "killed"`, `frontier__member_lost = true` · set `last_event = "bandit_killed_member"`, `party_alive = "{{ world.party_alive - 1 }}"` |
| 3 | [`frontier__bandits__flee`](#intent-frontier--bandits--flee) |  | [`robbery_aftermath`](#room-robbery-aftermath) | set `frontier__bandits__outcome = "fled"` · invoke `host.run.close` with `cmd = "true # fled"` · set `frontier__encounter_kind = "fled"` · set `last_event = "bandit_fled"` |
| 4 | [`frontier__bandits__look`](#intent-frontier--bandits--look) |  | `.` |  |
| 5 | [`frontier__bandits__pay`](#intent-frontier--bandits--pay) | `world.frontier__bandits__party_money >= world.frontier__bandits__threat_level * 50` | [`robbery_aftermath`](#room-robbery-aftermath) | set `frontier__bandits__outcome = "paid"`, `frontier__bandits__paid_amount = "{{ world.frontier__bandits__threat_level * 50 }}"` · invoke `host.run.close` with `cmd = "true # paid {{ world.frontier__bandits__threat_level * 50 }}"` · set `frontier__encounter_kind = "paid"`, `frontier__paid_amount = "{{ world.frontier__bandits__paid_amount }}"` · set `last_event = "bandit_resolved"`, `money = "{{ world.money - world.frontier__paid_amount }}"` |
| 6 | [`frontier__bandits__pay`](#intent-frontier--bandits--pay) | _default_ | `.` | _hint: Not enough money to buy them off._ |

### <a id="room-frontier-scouting"></a> `frontier.scouting`

A scout rides ahead to look the trail over.

**Shows world**: `frontier__party_alive`, `frontier__party_money`, `frontier__threat_level`, `frontier__scouting_intel`, `frontier__scout_finding`

**On enter**:

1. invoke `host.run.announce` with `cmd = "true # trail-flavoured scouting"`, `prompt = "/home/cloud-user/code/kitsoki/.worktrees/agent-split/stories/oregon-trail/prompts/scout_brief_trail.md"`

**Transitions**:

| # | Intent | Guard | → | Effects |
|---|---|---|---|---|
| 1 | [`frontier__look`](#intent-frontier--look) |  | `.` |  |
| 2 | [`frontier__proceed`](#intent-frontier--proceed) |  | `../bandits` |  |
| 3 | [`frontier__scout`](#intent-frontier--scout) |  | `.` | set `frontier__scout_finding = "Dust on the rise; riders in oilcloth, by the look of them."` |

### <a id="room-general-store"></a> `general_store`  _(compound)_

Matt's General Store, Independence — outfit the wagon before leaving.

**Initial child**: `idle`

**Shows world**: `money`, `oxen`, `food_lbs`, `bullets`, `clothing_sets`, `spare_wheels`, `spare_axles`, `spare_tongues`, `local_price_pct`, `proposal_items`, `proposal_total_cost`, `proposal_refine_count`

**On enter**:

1. set `local_price_pct = 100`

### <a id="room-general-store-compose"></a> `general_store.compose`

Compose a kit item-by-item (numbers only).

**Transitions**:

| # | Intent | Guard | → | Effects |
|---|---|---|---|---|
| 1 | [`back`](#intent-back) |  | `../idle` |  |
| 2 | [`look`](#intent-look) |  | `.` |  |
| 3 | [`propose_kit`](#intent-propose-kit) | `world.money >= int((int(slots.oxen ?? 0) * 40 + int(slots.food ?? 0) * 0.2 + int(slots.bullets ?? 0) * 2 + int(slots.clothing ?? 0) * 10 + int(slots.wheels ?? 0) * 10 + int(slots.axles ?? 0) * 10 + int(slots.tongues ?? 0) * 10) * world.local_price_pct / 100)` | `../reviewing` | set `proposal_axles = "{{ int(slots.axles ?? 0) }}"`, `proposal_bullets = "{{ int(slots.bullets ?? 0) }}"`, `proposal_clothing = "{{ int(slots.clothing ?? 0) }}"`, `proposal_food = "{{ int(slots.food ?? 0) }}"`, `proposal_items = "{{ (int(slots.oxen ?? 0) > 0 ? string(int(slots.oxen)) + \" oxen, \" : \"\") + (int(slots.food ?? 0) > 0 ? string(int(slots.food)) + \" lbs food, \" : \"\") + (int(slots.bullets ?? 0) > 0 ? string(int(slots.bullets)) + \" bullets, \" : \"\") + (int(slots.clothing ?? 0) > 0 ? string(int(slots.clothing)) + \" clothing, \" : \"\") + (int(slots.wheels ?? 0) > 0 ? string(int(slots.wheels)) + \" wheels, \" : \"\") + (int(slots.axles ?? 0) > 0 ? string(int(slots.axles)) + \" axles, \" : \"\") + (int(slots.tongues ?? 0) > 0 ? string(int(slots.tongues)) + \" tongues\" : \"\") }}"`, `proposal_oxen = "{{ int(slots.oxen ?? 0) }}"`, `proposal_refine_count = 0`, `proposal_tongues = "{{ int(slots.tongues ?? 0) }}"`, `proposal_total_cost = "{{ int((int(slots.oxen ?? 0) * 40 + int(slots.food ?? 0) * 0.2 + int(slots.bullets ?? 0) * 2 + int(slots.clothing ?? 0) * 10 + int(slots.wheels ?? 0) * 10 + int(slots.axles ?? 0) * 10 + int(slots.tongues ?? 0) * 10) * world.local_price_pct / 100) }}"`, `proposal_wheels = "{{ int(slots.wheels ?? 0) }}"` · say "Matt counts the slate. 'Total comes to ${{ world.proposal_total_cost }} — speak up if I've got it wrong.'" |
| 4 | [`propose_kit`](#intent-propose-kit) | _default_ | `.` | _hint: Not enough cash for that kit._ · say "Matt looks back at you. 'You haven't the money for that kit, friend.'" |

### <a id="room-general-store-describe"></a> `general_store.describe`

Matt walks you through what's on the shelves.

**Transitions**:

| # | Intent | Guard | → | Effects |
|---|---|---|---|---|
| 1 | [`back`](#intent-back) |  | `../idle` |  |
| 2 | [`look`](#intent-look) |  | `.` |  |
| 3 | [`open_compose`](#intent-open-compose) |  | `../compose` |  |
| 4 | [`propose_budget`](#intent-propose-budget) | `int(slots.total_cost) >= 1 && world.money >= int(slots.total_cost)` | `../reviewing` | set `proposal_axles = "{{ int(slots.total_cost) >= 200 ? 1 : 0 }}"`, `proposal_bullets = "{{ int(int(slots.total_cost) * 10 / 100 / 2) }}"`, `proposal_clothing = 0`, `proposal_food = "{{ int(int(slots.total_cost) * 50 / 100 / 0.2) }}"`, `proposal_items = "Matt's balanced kit"`, `proposal_oxen = "{{ int(int(slots.total_cost) * 30 / 100 / 40) }}"`, `proposal_refine_count = 0`, `proposal_tongues = 0`, `proposal_total_cost = "{{ int(slots.total_cost) }}"`, `proposal_wheels = "{{ int(slots.total_cost) >= 100 ? 1 : 0 }}"` · say "Matt scribbles for a minute and slides the slate across. 'Balanced kit for ${{ slots.total_cost }} — proposed.'" |
| 5 | [`propose_budget`](#intent-propose-budget) | _default_ | `.` | _hint: Budget must be positive and within your cash._ · say "Matt shakes his head — 'budget doesn't work, friend.'" |

### <a id="room-general-store-done"></a> `general_store.done`

Purchase complete.

**Shows world**: `proposal_items`, `proposal_total_cost`, `money`, `oxen`, `food_lbs`

**On enter**:

1. invoke `host.transport.post` with `body = "Bought {{ world.proposal_items }} for ${{ world.proposal_total_cost }} at Matt's. ${{ world.money }} cash on hand, {{ world.oxen }} oxen yoked, {{ world.food_lbs }} lbs of food in the wagon."`, `phase_id = "general_store_done_{{ world.proposal_items }}_{{ world.proposal_total_cost }}"`, `thread = "{{ run.id }}"`, `title = "Wagon loaded at Independence"`, `transport = "tui"`

**Transitions**:

| # | Intent | Guard | → | Effects |
|---|---|---|---|---|
| 1 | [`leave_store`](#intent-leave-store) | `world.oxen >= 2 && world.food_lbs >= 200` | [`leg_a_executing`](#room-leg-a-executing) | set `proposal_axles = 0`, `proposal_bullets = 0`, `proposal_clothing = 0`, `proposal_food = 0`, `proposal_items = ""`, `proposal_oxen = 0`, `proposal_refine_count = 0`, `proposal_tongues = 0`, `proposal_total_cost = 0`, `proposal_wheels = 0` · say "Matt walks out from behind the counter, claps the lead ox on the shoulder, and waves the wagon onto the road west. 'Mind the river bottoms.'" |
| 2 | [`leave_store`](#intent-leave-store) | _default_ | `../idle` | _hint: Still need 2 oxen and 200 lbs food before leaving._ · say "Matt eyes your wagon. 'Need more before you go.'" |
| 3 | [`look`](#intent-look) |  | `.` |  |
| 4 | [`repeat_purchase`](#intent-repeat-purchase) |  | `../idle` | set `proposal_axles = 0`, `proposal_bullets = 0`, `proposal_clothing = 0`, `proposal_food = 0`, `proposal_items = ""`, `proposal_oxen = 0`, `proposal_refine_count = 0`, `proposal_tongues = 0`, `proposal_total_cost = 0`, `proposal_wheels = 0` · say "Matt pulls a fresh leaf of paper off the spike. 'Right then — what else are we writing down?'" |

### <a id="room-general-store-idle"></a> `general_store.idle`

At Matt's counter — pick how to draft a purchase, or leave.

**Transitions**:

| # | Intent | Guard | → | Effects |
|---|---|---|---|---|
| 1 | [`browse_items`](#intent-browse-items) |  | `../describe` |  |
| 2 | [`leave_store`](#intent-leave-store) | `world.oxen >= 2 && world.food_lbs >= 200` | [`leg_a_executing`](#room-leg-a-executing) | set `proposal_axles = 0`, `proposal_bullets = 0`, `proposal_clothing = 0`, `proposal_food = 0`, `proposal_items = ""`, `proposal_oxen = 0`, `proposal_refine_count = 0`, `proposal_tongues = 0`, `proposal_total_cost = 0`, `proposal_wheels = 0` · say "Matt walks out from behind the counter, claps the lead ox on the shoulder, and waves the wagon onto the road west. 'Mind the river bottoms.'" |
| 3 | [`leave_store`](#intent-leave-store) | _default_ | `.` | _hint: Need at least 2 oxen and 200 lbs of food before leaving._ · say "Matt eyes your wagon. 'You won't make it to Kansas with that load — at least 2 oxen and 200 lbs of food.'" |
| 4 | [`look`](#intent-look) |  | `.` |  |
| 5 | [`open_compose`](#intent-open-compose) |  | `../compose` |  |
| 6 | [`propose_budget`](#intent-propose-budget) | `int(slots.total_cost) >= 1 && world.money >= int(slots.total_cost)` | `../reviewing` | set `proposal_axles = "{{ int(slots.total_cost) >= 200 ? 1 : 0 }}"`, `proposal_bullets = "{{ int(int(slots.total_cost) * 10 / 100 / 2) }}"`, `proposal_clothing = 0`, `proposal_food = "{{ int(int(slots.total_cost) * 50 / 100 / 0.2) }}"`, `proposal_items = "Matt's balanced kit"`, `proposal_oxen = "{{ int(int(slots.total_cost) * 30 / 100 / 40) }}"`, `proposal_refine_count = 0`, `proposal_tongues = 0`, `proposal_total_cost = "{{ int(slots.total_cost) }}"`, `proposal_wheels = "{{ int(slots.total_cost) >= 100 ? 1 : 0 }}"` · say "Matt scribbles for a minute and slides the slate across. 'Balanced kit for ${{ slots.total_cost }} — proposed.'" |
| 7 | [`propose_budget`](#intent-propose-budget) | _default_ | `.` | _hint: Budget must be a positive integer and within your cash on hand._ · say "Matt looks at the slate then back at you. 'That budget won't work, friend — check your cash on hand.'" |
| 8 | [`propose_purchase`](#intent-propose-purchase) | `int(slots.total_cost) < 5 && world.money >= int(slots.total_cost)` | `../done` | set `bullets = "{{ world.bullets + int(slots.bullets ?? 0) }}"`, `clothing_sets = "{{ world.clothing_sets + int(slots.clothing ?? 0) }}"`, `food_lbs = "{{ world.food_lbs + int(slots.food ?? 0) }}"`, `money = "{{ world.money - int(slots.total_cost) }}"`, `oxen = "{{ world.oxen + int(slots.oxen ?? 0) }}"`, `proposal_axles = "{{ int(slots.axles ?? 0) }}"`, `proposal_bullets = "{{ int(slots.bullets ?? 0) }}"`, `proposal_clothing = "{{ int(slots.clothing ?? 0) }}"`, `proposal_food = "{{ int(slots.food ?? 0) }}"`, `proposal_items = "{{ slots.items }}"`, `proposal_oxen = "{{ int(slots.oxen ?? 0) }}"`, `proposal_refine_count = 0`, `proposal_tongues = "{{ int(slots.tongues ?? 0) }}"`, `proposal_total_cost = "{{ int(slots.total_cost) }}"`, `proposal_wheels = "{{ int(slots.wheels ?? 0) }}"`, `spare_axles = "{{ world.spare_axles + int(slots.axles ?? 0) }}"`, `spare_tongues = "{{ world.spare_tongues + int(slots.tongues ?? 0) }}"`, `spare_wheels = "{{ world.spare_wheels + int(slots.wheels ?? 0) }}"` · say "Matt rings it up without a word — {{ slots.items }}, ${{ slots.total_cost }}. Hands it across the counter." |
| 9 | [`propose_purchase`](#intent-propose-purchase) | `world.money >= int(slots.total_cost)` | `../reviewing` | set `proposal_axles = "{{ int(slots.axles ?? 0) }}"`, `proposal_bullets = "{{ int(slots.bullets ?? 0) }}"`, `proposal_clothing = "{{ int(slots.clothing ?? 0) }}"`, `proposal_food = "{{ int(slots.food ?? 0) }}"`, `proposal_items = "{{ slots.items }}"`, `proposal_oxen = "{{ int(slots.oxen ?? 0) }}"`, `proposal_refine_count = 0`, `proposal_tongues = "{{ int(slots.tongues ?? 0) }}"`, `proposal_total_cost = "{{ int(slots.total_cost) }}"`, `proposal_wheels = "{{ int(slots.wheels ?? 0) }}"` · say "Matt licks his pencil and writes it down. 'So that's {{ slots.items }} — comes to ${{ slots.total_cost }}. Speak up if I've got it wrong.'" |
| 10 | [`propose_purchase`](#intent-propose-purchase) | _default_ | `.` | _hint: Not enough cash for that basket._ · say "Matt shakes his head. 'You haven't got the money for that, friend.'" |

### <a id="room-general-store-reviewing"></a> `general_store.reviewing`

Matt reads back the order.

**Shows world**: `proposal_items`, `proposal_total_cost`, `money`, `proposal_refine_count`

**Transitions**:

| # | Intent | Guard | → | Effects |
|---|---|---|---|---|
| 1 | [`accept_purchase`](#intent-accept-purchase) |  | `../done` | set `bullets = "{{ world.bullets + world.proposal_bullets }}"`, `clothing_sets = "{{ world.clothing_sets + world.proposal_clothing }}"`, `food_lbs = "{{ world.food_lbs + world.proposal_food }}"`, `money = "{{ world.money - world.proposal_total_cost }}"`, `oxen = "{{ world.oxen + world.proposal_oxen }}"`, `spare_axles = "{{ world.spare_axles + world.proposal_axles }}"`, `spare_tongues = "{{ world.spare_tongues + world.proposal_tongues }}"`, `spare_wheels = "{{ world.spare_wheels + world.proposal_wheels }}"` · say "Matt totals the slate and dusts his hands on his apron. 'Obliged for the ${{ world.proposal_total_cost }}. {{ world.proposal_items }} — all yours.' He nudges the parcel across." |
| 2 | [`cancel_purchase`](#intent-cancel-purchase) |  | `../idle` | set `proposal_axles = 0`, `proposal_bullets = 0`, `proposal_clothing = 0`, `proposal_food = 0`, `proposal_items = ""`, `proposal_oxen = 0`, `proposal_refine_count = 0`, `proposal_tongues = 0`, `proposal_total_cost = 0`, `proposal_wheels = 0` · say "Matt nods, drops the slate under the counter, and goes back to sorting nails. 'Take your time.'" |
| 3 | [`look`](#intent-look) |  | `.` |  |
| 4 | [`refine_purchase`](#intent-refine-purchase) |  | `../reviewing` | set `proposal_axles = "{{ int(slots.axles    ?? world.proposal_axles) }}"`, `proposal_bullets = "{{ int(slots.bullets  ?? world.proposal_bullets) }}"`, `proposal_clothing = "{{ int(slots.clothing ?? world.proposal_clothing) }}"`, `proposal_food = "{{ int(slots.food     ?? world.proposal_food) }}"`, `proposal_items = "{{ slots.items ?? world.proposal_items }}"`, `proposal_oxen = "{{ int(slots.oxen     ?? world.proposal_oxen) }}"`, `proposal_refine_count = "{{ world.proposal_refine_count + 1 }}"`, `proposal_tongues = "{{ int(slots.tongues  ?? world.proposal_tongues) }}"`, `proposal_total_cost = "{{ int(slots.total_cost ?? world.proposal_total_cost) }}"`, `proposal_wheels = "{{ int(slots.wheels   ?? world.proposal_wheels) }}"` · say "Matt licks his pencil, scratches out the line, writes the new one. 'Reckon that better suits you?' (revision {{ world.proposal_refine_count }})" |

### <a id="room-hunt"></a> `hunt`  _(compound)_

Hunting expedition.

**Initial child**: `hunt_idle`

**Shows world**: `bullets`, `food_lbs`, `month`, `party_alive`, `last_job_id`, `last_hunt_lbs`, `last_hunt_target`, `last_hunt_outcome`, `pending_bullet_spend`, `current_landmark`

**On enter**:

1. set `last_hunt_lbs = 0`, `last_hunt_outcome = ""`, `last_hunt_target = ""`, `pending_bullet_spend = 0`

### <a id="room-hunt-hunt-done"></a> `hunt.hunt_done`

Hunting party returned.

**Shows world**: `last_hunt_lbs`, `last_hunt_target`, `last_hunt_outcome`, `food_lbs`, `bullets`, `current_landmark`

**Transitions**:

| # | Intent | Guard | → | Effects |
|---|---|---|---|---|
| 1 | [`continue`](#intent-continue) | `world.current_landmark == 'Kansas River Crossing'` | [`leg_a_awaiting_reply`](#room-leg-a-awaiting-reply) |  |
| 2 | [`continue`](#intent-continue) | `world.current_landmark == 'Fort Kearney'` | [`leg_b_awaiting_reply`](#room-leg-b-awaiting-reply) |  |
| 3 | [`continue`](#intent-continue) | `world.current_landmark == 'Chimney Rock'` | [`leg_c_awaiting_reply`](#room-leg-c-awaiting-reply) |  |
| 4 | [`continue`](#intent-continue) | `world.current_landmark == 'Fort Laramie'` | [`leg_d_awaiting_reply`](#room-leg-d-awaiting-reply) |  |
| 5 | [`continue`](#intent-continue) | `world.current_landmark == 'South Pass'` | [`leg_e_awaiting_reply`](#room-leg-e-awaiting-reply) |  |
| 6 | [`continue`](#intent-continue) | `world.current_landmark == 'Snake River Crossing'` | [`leg_f_awaiting_reply`](#room-leg-f-awaiting-reply) |  |
| 7 | [`continue`](#intent-continue) | `world.current_landmark == 'Willamette Valley'` | [`leg_g_awaiting_reply`](#room-leg-g-awaiting-reply) |  |
| 8 | [`continue`](#intent-continue) | _default_ | [`leg_a_awaiting_reply`](#room-leg-a-awaiting-reply) | _hint: Unknown landmark — returning to the first leg._ |
| 9 | [`look`](#intent-look) |  | `.` |  |

### <a id="room-hunt-hunt-idle"></a> `hunt.hunt_idle`

Choosing how many bullets to spend.

**Shows world**: `bullets`, `food_lbs`, `month`, `current_landmark`

**Transitions**:

| # | Intent | Guard | → | Effects |
|---|---|---|---|---|
| 1 | [`check_inbox`](#intent-check-inbox) |  | [`inbox`](#room-inbox) | set `last_job_originating_state = "hunt.hunt_idle"` · _(no-history)_ |
| 2 | [`look`](#intent-look) |  | `.` |  |
| 3 | [`shoot`](#intent-shoot) | `int(slots.bullets) > 0 && int(slots.bullets) <= world.bullets` | `../hunt_running` | set `pending_bullet_spend = "{{ int(slots.bullets) }}"` · say "Hunting party heads out with {{ slots.bullets }} bullets." |
| 4 | [`shoot`](#intent-shoot) | _default_ | `.` | _hint: Need between 1 and {{ world.bullets }} bullets to hunt._ · say "Can't spend {{ slots.bullets }} bullets — you only have {{ world.bullets }}." |

### <a id="room-hunt-hunt-running"></a> `hunt.hunt_running`

Hunting party is out.

**Shows world**: `last_job_id`, `pending_bullet_spend`, `current_landmark`, `last_hunt_outcome`

**On enter**:

1. set `last_job_originating_state = "hunt.hunt_running"`
2. invoke `host.run` with `cmd = "echo '{\"lbs\":85,\"target\":\"bison\",\"outcome\":\"success\"}'"`, bind `last_job_id ← job_id`

**Transitions**:

| # | Intent | Guard | → | Effects |
|---|---|---|---|---|
| 1 | [`answer_clarification`](#intent-answer-clarification) |  | `.` | invoke `host.jobs.answer_clarification` with `answer = "{{ slots.answer }}"`, `job_id = "{{ slots.job_id }}"` · say "Submitted target choice to the hunting party." |
| 2 | [`check_inbox`](#intent-check-inbox) |  | [`inbox`](#room-inbox) | set `last_job_originating_state = "hunt.hunt_running"` · _(no-history)_ |
| 3 | [`continue`](#intent-continue) | `world.last_hunt_outcome != ''` | `../hunt_done` |  |
| 4 | [`continue`](#intent-continue) | _default_ | `.` | _hint: Hunting party is still out — wait for them to return._ |
| 5 | [`look`](#intent-look) |  | `.` |  |

### <a id="room-inbox"></a> `inbox`

Inbox — pending background-job notifications.

**Shows world**: `inbox_unread`, `last_job_id`, `last_hunt_target`, `last_hunt_lbs`, `last_hunt_outcome`, `last_job_originating_state`

**Transitions**:

| # | Intent | Guard | → | Effects |
|---|---|---|---|---|
| 1 | [`answer_clarification`](#intent-answer-clarification) |  | `{{ world.last_job_originating_state }}` | invoke `host.jobs.answer_clarification` with `answer = "{{ slots.answer }}"`, `job_id = "{{ slots.job_id }}"` · set `inbox_unread = 0` · say "Answered clarification for job {{ slots.job_id }}." · _(no-history)_ |
| 2 | [`look`](#intent-look) |  | `.` |  |
| 3 | [`open_job`](#intent-open-job) |  | `{{ world.last_job_originating_state }}` | set `inbox_unread = 0` · say "Returning to {{ world.last_job_originating_state }}." · _(no-history)_ |

### <a id="room-intro"></a> `intro`  _(root)_

Independence, Missouri — overview and setup.

**Shows world**: `year`

**Transitions**:

| # | Intent | Guard | → | Effects |
|---|---|---|---|---|
| 1 | [`begin_setup`](#intent-begin-setup) |  | [`intro_profession`](#room-intro-profession) |  |
| 2 | [`look`](#intent-look) |  | [`intro`](#room-intro) |  |

### <a id="room-intro-month"></a> `intro_month`

Pick departure month.

**Shows world**: `profession`, `month`

**Transitions**:

| # | Intent | Guard | → | Effects |
|---|---|---|---|---|
| 1 | [`back`](#intent-back) |  | [`intro_profession`](#room-intro-profession) |  |
| 2 | [`look`](#intent-look) |  | [`intro_month`](#room-intro-month) |  |
| 3 | [`pick_month`](#intent-pick-month) |  | [`intro_party_names`](#room-intro-party-names) | set `month = "{{ slots.month }}"` · say "Departure month set to {{ slots.month }}." |

### <a id="room-intro-party-names"></a> `intro_party_names`

Name your wagon party of five.

**Shows world**: `party_names`, `party_size`, `party_member_1`, `party_member_2`, `party_member_3`, `party_member_4`, `party_member_5`

**Transitions**:

| # | Intent | Guard | → | Effects |
|---|---|---|---|---|
| 1 | [`back`](#intent-back) |  | [`intro_month`](#room-intro-month) |  |
| 2 | [`continue`](#intent-continue) | `world.party_names != ''` | [`intro_summary`](#room-intro-summary) |  |
| 3 | [`continue`](#intent-continue) | `world.party_member_1 != ''` | [`intro_summary`](#room-intro-summary) | set `party_names = "{{ world.party_member_1 }},{{ world.party_member_2 }},{{ world.party_member_3 }},{{ world.party_member_4 }},{{ world.party_member_5 }}"` |
| 4 | [`continue`](#intent-continue) | _default_ | [`intro_party_names`](#room-intro-party-names) | _hint: Name at least the leader (member 1) before continuing._ · say "Name at least the leader (member 1) before you continue." |
| 5 | [`generate_names`](#intent-generate-names) | `world.narration` | [`intro_party_names`](#room-intro-party-names) | invoke `host.agent.decide` with `agent = "party_namer"`, `args = map[theme:{{ slots.theme }}]`, `prompt = "prompts/name_party.md"`, `schema = "mcp/party_names.json"`, bind `party_member_1 ← submitted.names[0]`, `party_member_2 ← submitted.names[1]`, `party_member_3 ← submitted.names[2]`, `party_member_4 ← submitted.names[3]`, `party_member_5 ← submitted.names[4]`, `party_names ← {{ join(result.submitted.names, ',') }}`, `party_names_list ← submitted.names`, on_error → `intro_party_names` · say "Named the wagon party from theme: {{ slots.theme }}." |
| 6 | [`generate_names`](#intent-generate-names) | _default_ | [`intro_party_names`](#room-intro-party-names) | set `party_names = "{{ hasPrefix(lower(slots.theme), \"west\") ? \"Hank,Jesse,Mary,Ezra,Sarah\" : (hasPrefix(lower(slots.theme), \"star wars\") ? \"Luke,Leia,Han,Chewie,Yoda\" : (hasPrefix(lower(slots.theme), \"norse\") ? \"Erik,Helga,Thor,Sigrid,Bjorn\" : (hasPrefix(lower(slots.theme), \"lord of the rings\") ? \"Frodo,Sam,Merry,Pippin,Bilbo\" : \"Adam,Beth,Carol,Daniel,Edith\"))) }}"` · set `party_member_1 = "{{ trim(split(world.party_names, \",\")[0]) }}"`, `party_member_2 = "{{ trim(split(world.party_names, \",\")[1]) }}"`, `party_member_3 = "{{ trim(split(world.party_names, \",\")[2]) }}"`, `party_member_4 = "{{ trim(split(world.party_names, \",\")[3]) }}"`, `party_member_5 = "{{ trim(split(world.party_names, \",\")[4]) }}"` · say "Named the wagon party from theme: {{ slots.theme }} → {{ world.party_names }}." |
| 7 | [`look`](#intent-look) |  | [`intro_party_names`](#room-intro-party-names) |  |
| 8 | [`name_member`](#intent-name-member) | `slots.index == 1` | [`intro_party_names`](#room-intro-party-names) | set `party_member_1 = "{{ slots.name }}"` · say "Member 1 (leader) named {{ slots.name }}." |
| 9 | [`name_member`](#intent-name-member) | `slots.index == 2` | [`intro_party_names`](#room-intro-party-names) | set `party_member_2 = "{{ slots.name }}"` · say "Member 2 named {{ slots.name }}." |
| 10 | [`name_member`](#intent-name-member) | `slots.index == 3` | [`intro_party_names`](#room-intro-party-names) | set `party_member_3 = "{{ slots.name }}"` · say "Member 3 named {{ slots.name }}." |
| 11 | [`name_member`](#intent-name-member) | `slots.index == 4` | [`intro_party_names`](#room-intro-party-names) | set `party_member_4 = "{{ slots.name }}"` · say "Member 4 named {{ slots.name }}." |
| 12 | [`name_member`](#intent-name-member) | `slots.index == 5` | [`intro_party_names`](#room-intro-party-names) | set `party_member_5 = "{{ slots.name }}"` · say "Member 5 named {{ slots.name }}." |
| 13 | [`name_member`](#intent-name-member) | _default_ | [`intro_party_names`](#room-intro-party-names) | _hint: Index must be 1..{{ world.party_size }}._ · say "Index {{ slots.index }} is out of range — pick 1..{{ world.party_size }}." |
| 14 | [`name_party`](#intent-name-party) |  | [`intro_party_names`](#room-intro-party-names) | set `party_member_1 = "{{ trim(split(slots.names, \",\")[0]) }}"`, `party_member_2 = "{{ trim(split(slots.names, \",\")[1]) }}"`, `party_member_3 = "{{ trim(split(slots.names, \",\")[2]) }}"`, `party_member_4 = "{{ trim(split(slots.names, \",\")[3]) }}"`, `party_member_5 = "{{ trim(split(slots.names, \",\")[4]) }}"`, `party_names = "{{ slots.names }}"` · say "Party named: {{ slots.names }}." |

### <a id="room-intro-profession"></a> `intro_profession`

Pick your profession — sets starting cash and score multiplier.

**Shows world**: `profession`, `money`

**Transitions**:

| # | Intent | Guard | → | Effects |
|---|---|---|---|---|
| 1 | [`back`](#intent-back) |  | [`intro`](#room-intro) |  |
| 2 | [`look`](#intent-look) |  | [`intro_profession`](#room-intro-profession) |  |
| 3 | [`pick_profession`](#intent-pick-profession) |  | [`intro_month`](#room-intro-month) | set `money = "{{ slots.profession == 'banker' ? 1600 : (slots.profession == 'carpenter' ? 800 : 400) }}"`, `profession = "{{ slots.profession }}"` · say "Profession set to {{ slots.profession }}; starting cash ${{ world.money }}." |

### <a id="room-intro-summary"></a> `intro_summary`

Confirm setup and depart.

**Shows world**: `profession`, `month`, `party_names`, `money`

**Transitions**:

| # | Intent | Guard | → | Effects |
|---|---|---|---|---|
| 1 | [`edit_step`](#intent-edit-step) | `slots.step == 'profession'` | [`intro_profession`](#room-intro-profession) |  |
| 2 | [`edit_step`](#intent-edit-step) | `slots.step == 'month'` | [`intro_month`](#room-intro-month) |  |
| 3 | [`edit_step`](#intent-edit-step) | `slots.step == 'names'` | [`intro_party_names`](#room-intro-party-names) |  |
| 4 | [`look`](#intent-look) |  | [`intro_summary`](#room-intro-summary) |  |
| 5 | [`start_journey`](#intent-start-journey) | `world.party_names != '' && world.profession != nil && world.month != nil` | [`general_store`](#room-general-store) | say "Wagon's hitched. Off to the general store." |
| 6 | [`start_journey`](#intent-start-journey) | _default_ | [`intro_summary`](#room-intro-summary) | _hint: Setup isn't complete — return to an earlier step._ · say "Setup isn't complete. Use Change X to return to an earlier step." |

### <a id="room-leg-a-awaiting-reply"></a> `leg_a_awaiting_reply`

Arrived at Kansas River Crossing (prairie).

**Shows world**: `day`, `miles_traveled`, `food_lbs`, `oxen`, `party_alive`, `health_avg`, `illness_kind`, `illness_severity`, `illness_member`, `last_landmark_prose`

**On enter**:

1. set `last_landmark_prose = ""`
2. invoke `host.transport.post` with `body = "Day {{ world.day }}, {{ world.month }} {{ world.year }}. We rolled into **Kansas River Crossing** (prairie) at last.\n\n- Food: {{ world.food_lbs }} lbs\n- Oxen: {{ world.oxen }}\n- Party: {{ world.party_alive }} alive\n- Health: {{ world.health_avg }}\n"`, `phase_id = "leg_a_arrival"`, `thread = "{{ run.id }}"`, `title = "Day {{ world.day }}: Kansas River Crossing"`, `transport = "tui"`
3. invoke `host.agent.ask` with `agent = "trail_narrator"`, `args = map[day:{{ world.day }} food_lbs:{{ world.food_lbs }} landmark:Kansas River Crossing miles_traveled:{{ world.miles_traveled }} month:{{ world.month }} party_alive:{{ world.party_alive }} year:{{ world.year }}]`, `prompt_path = "prompts/landmark_arrival.md"`, bind `last_landmark_prose ← stdout`

**Transitions**:

| # | Intent | Guard | → | Effects |
|---|---|---|---|---|
| 1 | [`approach_river`](#intent-approach-river) | `true` | [`river_crossing`](#room-river-crossing) | set `current_landmark = "Kansas River Crossing"`, `river_depth_ft = "{{ int(2 * (world.month == 'april' ? 160 : (world.month == 'march' ? 140 : (world.month == 'may' ? 130 : (world.month == 'june' ? 100 : (world.month == 'july' ? 80 : (world.month == 'august' ? 70 : (world.month == 'september' ? 80 : 100))))))) / 100) }}"`, `river_width_ft = "{{ int(620) }}"` |
| 2 | [`approach_river`](#intent-approach-river) | _default_ | `.` | _hint: No river at this landmark._ |
| 3 | [`consult_guide`](#intent-consult-guide) |  | [`trail_guide`](#room-trail-guide) | set `last_job_originating_state = "leg_a_awaiting_reply"` · _(no-history)_ |
| 4 | [`continue`](#intent-continue) | `'Kansas River Crossing' == 'South Pass' && (world.month == 'october' \|\| world.month == 'november' \|\| world.month == 'december' \|\| world.month == 'january' \|\| world.month == 'february')` | [`snow_blocked`](#room-snow-blocked) | set `current_landmark = "Kansas River Crossing"` · say "South Pass is snowed in. The wagons cannot get through." |
| 5 | [`continue`](#intent-continue) | _default_ | [`leg_b_executing`](#room-leg-b-executing) | set `breakdown_part = ""`, `current_event_attempts = 0`, `current_landmark = "Kansas River Crossing"`, `encounter_kind = ""`, `event_kind = ""`, `illness_kind = ""`, `miles_traveled = 0`, `weather_kind = ""` |
| 6 | [`enter_fort`](#intent-enter-fort) | `false` | [`fort`](#room-fort) | set `current_landmark = "Kansas River Crossing"` |
| 7 | [`enter_fort`](#intent-enter-fort) | _default_ | `.` | _hint: No fort at this landmark._ |
| 8 | [`face_robbery`](#intent-face-robbery) |  | [`frontier`](#room-frontier) |  |
| 9 | [`give_up_leg`](#intent-give-up-leg) | `world.cycle__leg_a__on_failure < 2` | [`leg_a_executing`](#room-leg-a-executing) | increment `cycle__leg_a__on_failure += 1` · set `breakdown_part = ""`, `current_event_attempts = 0`, `encounter_kind = ""`, `event_kind = ""`, `illness_kind = ""`, `miles_traveled = 0`, `weather_kind = ""` · say "The party turns back toward Independence." |
| 10 | [`give_up_leg`](#intent-give-up-leg) | _default_ | [`leg_a_error`](#room-leg-a-error) | say "The party has given up too many times — stranded." |
| 11 | [`hunt`](#intent-hunt) |  | [`hunt`](#room-hunt) |  |
| 12 | [`quit`](#intent-quit) |  | [`ended_lost`](#room-ended-lost) | say "The party abandons the journey at Kansas River Crossing." |
| 13 | [`rest`](#intent-rest) |  | [`rest_room`](#room-rest-room) |  |
| 14 | [`restart_from`](#intent-restart-from) | `slots.stage == 'independence' \|\| slots.stage == 'kansas'` | [`leg_a_executing`](#room-leg-a-executing) | set `breakdown_part = ""`, `current_event_attempts = 0`, `encounter_kind = ""`, `event_kind = ""`, `illness_kind = ""`, `miles_traveled = 0`, `weather_kind = ""` · say "The party doubles back to Independence to retry the run toward Kansas River." |
| 15 | [`restart_from`](#intent-restart-from) | `slots.stage == 'kearney'` | [`leg_b_executing`](#room-leg-b-executing) | set `breakdown_part = ""`, `current_event_attempts = 0`, `encounter_kind = ""`, `event_kind = ""`, `illness_kind = ""`, `miles_traveled = 0`, `weather_kind = ""` · say "The party doubles back to Kansas River to retry the stretch toward Fort Kearney." |
| 16 | [`restart_from`](#intent-restart-from) | `slots.stage == 'chimney'` | [`leg_c_executing`](#room-leg-c-executing) | set `breakdown_part = ""`, `current_event_attempts = 0`, `encounter_kind = ""`, `event_kind = ""`, `illness_kind = ""`, `miles_traveled = 0`, `weather_kind = ""` · say "The party doubles back to Fort Kearney to retry the stretch toward Chimney Rock." |
| 17 | [`restart_from`](#intent-restart-from) | `slots.stage == 'laramie'` | [`leg_d_executing`](#room-leg-d-executing) | set `breakdown_part = ""`, `current_event_attempts = 0`, `encounter_kind = ""`, `event_kind = ""`, `illness_kind = ""`, `miles_traveled = 0`, `weather_kind = ""` · say "The party doubles back to Chimney Rock to retry the stretch toward Fort Laramie." |
| 18 | [`restart_from`](#intent-restart-from) | `slots.stage == 'south_pass'` | [`leg_e_executing`](#room-leg-e-executing) | set `breakdown_part = ""`, `current_event_attempts = 0`, `encounter_kind = ""`, `event_kind = ""`, `illness_kind = ""`, `miles_traveled = 0`, `weather_kind = ""` · say "The party doubles back to Fort Laramie to retry the stretch toward South Pass." |
| 19 | [`restart_from`](#intent-restart-from) | `slots.stage == 'snake'` | [`leg_f_executing`](#room-leg-f-executing) | set `breakdown_part = ""`, `current_event_attempts = 0`, `encounter_kind = ""`, `event_kind = ""`, `illness_kind = ""`, `miles_traveled = 0`, `weather_kind = ""` · say "The party doubles back to South Pass to retry the stretch toward Snake River." |
| 20 | [`restart_from`](#intent-restart-from) | _default_ | `.` | _hint: Unknown restart stage._ |
| 21 | [`scout`](#intent-scout) |  | [`frontier`](#room-frontier) |  |

**Timeout**: after `10d` → `leg_b_executing`

### <a id="room-leg-a-error"></a> `leg_a_error`

Stranded between Independence and Kansas River Crossing.

**Shows world**: `day`, `party_alive`, `current_landmark`

**Transitions**:

| # | Intent | Guard | → | Effects |
|---|---|---|---|---|
| 1 | [`quit`](#intent-quit) |  | [`ended_lost`](#room-ended-lost) | say "The party gives up between Independence and Kansas River Crossing." |
| 2 | [`retry`](#intent-retry) |  | [`leg_a_executing`](#room-leg-a-executing) | set `current_event_attempts = 0`, `event_kind = ""` |

### <a id="room-leg-a-executing"></a> `leg_a_executing`  _(compound)_

On the trail from Independence to Kansas River Crossing (prairie).

**Initial child**: `traveling`

**Shows world**: `day`, `month`, `miles_traveled`, `food_lbs`, `oxen`, `party_alive`, `health_avg`, `pace`, `rations`, `current_landmark`, `event_kind`, `illness_kind`, `illness_severity`, `illness_member`, `breakdown_part`, `weather_kind`, `encounter_kind`, `rng_last`, `rng_counter`

**On enter**:

1. set `current_landmark = "Independence"`

**Transitions**:

| # | Intent | Guard | → | Effects |
|---|---|---|---|---|
| 1 | [`on_failure`](#intent-on-failure) | `world.cycle__leg_a__on_failure < 2` | [`leg_a_executing`](#room-leg-a-executing) | increment `cycle__leg_a__on_failure += 1` |
| 2 | [`on_failure`](#intent-on-failure) | _default_ | [`leg_a_error`](#room-leg-a-error) | _hint: cycle budget exceeded for on_failure_ |

### <a id="room-leg-a-executing-event-breakdown"></a> `leg_a_executing.event_breakdown`

Wagon breakdown — {{ world.breakdown_part }}.

**Shows world**: `breakdown_part`, `spare_wheels`, `spare_axles`, `spare_tongues`, `day`, `current_event_attempts`, `last_event_prose`

**On enter**:

1. set `breakdown_part = "{{ if world.rng_last % 3 == 0 }}wheel{{ else }}{{ if world.rng_last % 3 == 1 }}axle{{ else }}tongue{{ end }}{{ end }}"`, `current_event_attempts = 0`, `last_event_prose = ""`
2. invoke `host.agent.ask` with `agent = "trail_narrator"`, `args = map[current_landmark:{{ world.current_landmark }} day:{{ world.day }} part:{{ if world.rng_last % 3 == 0 }}wheel{{ else }}{{ if world.rng_last % 3 == 1 }}axle{{ else }}tongue{{ end }}{{ end }} spares_remaining:{{ world.rng_last % 3 == 0 ? world.spare_wheels : (world.rng_last % 3 == 1 ? world.spare_axles : world.spare_tongues) }}]`, `prompt_path = "prompts/event_breakdown.md"`, bind `last_event_prose ← stdout`

**Transitions**:

| # | Intent | Guard | → | Effects |
|---|---|---|---|---|
| 1 | [`look`](#intent-look) |  | `.` |  |
| 2 | [`quit`](#intent-quit) |  | [`ended_lost`](#room-ended-lost) | say "The party abandons the broken wagon." |
| 3 | [`repair`](#intent-repair) | `world.breakdown_part == 'wheel' && world.spare_wheels >= 1` | `../traveling` | invoke `host.transport.post` with `body = "Day {{ world.day }} — fitted the spare wheel and got the wagon rolling again. One less in reserve."`, `phase_id = "leg_a_event_breakdown_wheel_repaired_{{ world.day }}"`, `thread = "{{ run.id }}"`, `title = "Wheel repaired"`, `transport = "tui"` · set `breakdown_part = ""`, `current_event_attempts = 0`, `event_kind = ""`, `spare_wheels = "{{ world.spare_wheels - 1 }}"` · say "Spare wheel installed. Back on the trail." |
| 4 | [`repair`](#intent-repair) | `world.breakdown_part == 'axle' && world.spare_axles >= 1` | `../traveling` | invoke `host.transport.post` with `body = "Day {{ world.day }} — swapped in the spare axle. Took the morning, but the wagon's true again."`, `phase_id = "leg_a_event_breakdown_axle_repaired_{{ world.day }}"`, `thread = "{{ run.id }}"`, `title = "Axle repaired"`, `transport = "tui"` · set `breakdown_part = ""`, `current_event_attempts = 0`, `event_kind = ""`, `spare_axles = "{{ world.spare_axles - 1 }}"` · say "Spare axle installed. Back on the trail." |
| 5 | [`repair`](#intent-repair) | `world.breakdown_part == 'tongue' && world.spare_tongues >= 1` | `../traveling` | invoke `host.transport.post` with `body = "Day {{ world.day }} — pinned in the spare tongue and we hitched the oxen back up."`, `phase_id = "leg_a_event_breakdown_tongue_repaired_{{ world.day }}"`, `thread = "{{ run.id }}"`, `title = "Tongue repaired"`, `transport = "tui"` · set `breakdown_part = ""`, `current_event_attempts = 0`, `event_kind = ""`, `spare_tongues = "{{ world.spare_tongues - 1 }}"` · say "Spare tongue installed. Back on the trail." |
| 6 | [`repair`](#intent-repair) | `world.current_event_attempts < 2` | `.` | _hint: Need a spare {{ world.breakdown_part }} to repair._ · increment `current_event_attempts += 1` · say "No spare {{ world.breakdown_part }} on hand. Try repair again, wait_out, or look." |
| 7 | [`repair`](#intent-repair) | _default_ | `../traveling` | invoke `host.transport.post` with `body = "Day {{ world.day }} — couldn't repair the {{ world.breakdown_part }}. Lashed it together and pressed on at a cost: a member of the party was left behind."`, `phase_id = "leg_a_event_breakdown_failed_{{ world.day }}"`, `thread = "{{ run.id }}"`, `title = "Wagon limping on"`, `transport = "tui"` · set `breakdown_part = ""`, `current_event_attempts = 0`, `event_kind = ""`, `health_avg = "{{ world.health_avg - 20 }}"`, `party_alive = "{{ world.party_alive - 1 }}"` · say "Repeated repair attempts failed; the wagon is patched poorly and the party limps on. A member is left behind." |
| 8 | [`wait_out`](#intent-wait-out) |  | `../traveling` | invoke `host.transport.post` with `body = "Day {{ world.day }} — made camp and improvised a {{ world.breakdown_part }} repair over five days. Slow going, but back on the trail."`, `phase_id = "leg_a_event_breakdown_waited_{{ world.day }}"`, `thread = "{{ run.id }}"`, `title = "Improvised repair"`, `transport = "tui"` · set `breakdown_part = ""`, `current_event_attempts = 0`, `day = "{{ world.day + 5 }}"`, `event_kind = ""` · say "The party makes camp and improvises a repair over five days." |

### <a id="room-leg-a-executing-event-disease"></a> `leg_a_executing.event_disease`

Illness has struck the party ({{ world.illness_kind }}).

**Shows world**: `illness_kind`, `illness_severity`, `illness_treatment`, `illness_member`, `health_avg`, `party_alive`, `food_lbs`, `clothing_sets`, `day`, `current_event_attempts`

**On enter**:

1. set `current_event_attempts = 0`, `health_avg = "{{ world.health_avg - 10 }}"`, `illness_member = "{{ split(world.party_names, ',')[world.rng_last % world.party_alive] }}"`
2. set `illness_kind = "{{ if world.rng_last % 5 == 0 }}dysentery{{ else }}{{ if world.rng_last % 5 == 1 }}cholera{{ else }}{{ if world.rng_last % 5 == 2 }}typhoid{{ else }}{{ if world.rng_last % 5 == 3 }}measles{{ else }}exhaustion{{ end }}{{ end }}{{ end }}{{ end }}"`, `illness_severity = "{{ world.rng_last % 5 + 1 }}"`, `illness_treatment = "rest"`
3. invoke `host.agent.decide` with `agent = "frontier_doctor"`, `args = map[clothing_sets:{{ world.clothing_sets }} current_landmark:{{ world.current_landmark }} food_lbs:{{ world.food_lbs }} health_avg:{{ world.health_avg }} party_alive:{{ world.party_alive }} rng_last:{{ world.rng_last }}]`, `prompt = "prompts/event_disease.md"`, `schema = "mcp/illness.json"`, bind `illness_kind ← submitted.illness`, `illness_severity ← submitted.severity`, `illness_treatment ← submitted.treatment`, on_error → `leg_a_error`

**Transitions**:

| # | Intent | Guard | → | Effects |
|---|---|---|---|---|
| 1 | [`look`](#intent-look) |  | `.` |  |
| 2 | [`move_on`](#intent-move-on) |  | `../traveling` | invoke `host.transport.post` with `body = "Day {{ world.day }} — pushed on through the {{ world.illness_kind }} rather than stop. Health is worse for it, but the wagon rolls."`, `phase_id = "leg_a_event_disease_moved_on_{{ world.day }}"`, `thread = "{{ run.id }}"`, `title = "Pressing on through illness"`, `transport = "tui"` · set `current_event_attempts = 0`, `event_kind = ""`, `health_avg = "{{ world.health_avg - 15 }}"`, `illness_kind = ""`, `illness_member = ""`, `illness_severity = 0`, `illness_treatment = ""` · say "The party presses on. Health worsens." |
| 3 | [`quit`](#intent-quit) |  | [`ended_lost`](#room-ended-lost) | say "The party gives up on the trail, broken by {{ world.illness_kind }}." |
| 4 | [`treat`](#intent-treat) | `world.clothing_sets >= 1 && world.food_lbs >= 50 && world.current_event_attempts < 2` | `../traveling` | invoke `host.transport.post` with `body = "Day {{ world.day }} — the {{ world.illness_kind }} has passed. Rested up, one clothing set used and 50 lbs of food spent on broth and care. Spirits steady, on the move."`, `phase_id = "leg_a_event_disease_treated_{{ world.day }}"`, `thread = "{{ run.id }}"`, `title = "Disease treated"`, `transport = "tui"` · set `clothing_sets = "{{ world.clothing_sets - 1 }}"`, `current_event_attempts = 0`, `event_kind = ""`, `food_lbs = "{{ world.food_lbs - 50 }}"`, `health_avg = "{{ world.health_avg + 20 }}"`, `illness_kind = ""`, `illness_member = ""`, `illness_severity = 0`, `illness_treatment = ""` · say "The party rests and treats the illness." |
| 5 | [`treat`](#intent-treat) | `(world.clothing_sets < 1 \|\| world.food_lbs < 50) && world.current_event_attempts < 2` | `.` | increment `current_event_attempts += 1` · say "Not enough supplies to treat the {{ world.illness_kind }} (need 1 clothing set + 50 lbs food). Try again or wait_out." |
| 6 | [`treat`](#intent-treat) | _default_ | `../traveling` | invoke `host.transport.post` with `body = "Day {{ world.day }} — after repeated attempts, the {{ world.illness_kind }} took one of the party. May the trail remember them."`, `phase_id = "leg_a_event_disease_lost_{{ world.day }}"`, `thread = "{{ run.id }}"`, `title = "Lost to illness"`, `transport = "tui"` · set `current_event_attempts = 0`, `event_kind = ""`, `illness_kind = ""`, `illness_member = ""`, `illness_severity = 0`, `illness_treatment = ""`, `party_alive = "{{ world.party_alive - 1 }}"` · say "After repeated futile attempts, a party member has died of illness." |
| 7 | [`wait_out`](#intent-wait-out) | `world.health_avg < 30` | `../traveling` | invoke `host.transport.post` with `body = "Day {{ world.day }} — the patient was too weak. {{ world.illness_kind }} claimed one of the party while we waited."`, `phase_id = "leg_a_event_disease_wait_lost_{{ world.day }}"`, `thread = "{{ run.id }}"`, `title = "Lost to illness"`, `transport = "tui"` · set `current_event_attempts = 0`, `day = "{{ world.day + 1 }}"`, `event_kind = ""`, `illness_kind = ""`, `illness_member = ""`, `illness_severity = 0`, `illness_treatment = ""`, `party_alive = "{{ world.party_alive - 1 }}"` · say "The patient was too weak. A party member dies of illness." |
| 8 | [`wait_out`](#intent-wait-out) | _default_ | `../traveling` | invoke `host.transport.post` with `body = "Day {{ world.day }} — one day of rest and the {{ world.illness_kind }} let go. We move on tomorrow."`, `phase_id = "leg_a_event_disease_wait_ok_{{ world.day }}"`, `thread = "{{ run.id }}"`, `title = "Illness passed"`, `transport = "tui"` · set `current_event_attempts = 0`, `day = "{{ world.day + 1 }}"`, `event_kind = ""`, `illness_kind = ""`, `illness_member = ""`, `illness_severity = 0`, `illness_treatment = ""` · say "The party rests a day. The illness passes." |

### <a id="room-leg-a-executing-event-encounter"></a> `leg_a_executing.event_encounter`

Encounter on the trail: {{ world.encounter_kind }}.

**Shows world**: `encounter_kind`, `food_lbs`, `clothing_sets`, `last_event_prose`

**On enter**:

1. set `current_event_attempts = 0`, `encounter_kind = "{{ if world.rng_last % 3 == 0 }}trader{{ else }}{{ if world.rng_last % 3 == 1 }}hunter{{ else }}band{{ end }}{{ end }}"`, `last_event_prose = ""`
2. invoke `host.agent.ask` with `agent = "trail_narrator"`, `args = map[clothing_sets:{{ world.clothing_sets }} current_landmark:{{ world.current_landmark }} day:{{ world.day }} food_lbs:{{ world.food_lbs }} kind:{{ if world.rng_last % 3 == 0 }}trader{{ else }}{{ if world.rng_last % 3 == 1 }}hunter{{ else }}band{{ end }}{{ end }}]`, `prompt_path = "prompts/event_encounter.md"`, bind `last_event_prose ← stdout`

**Transitions**:

| # | Intent | Guard | → | Effects |
|---|---|---|---|---|
| 1 | [`accept_trade`](#intent-accept-trade) | `world.food_lbs >= 50` | `../traveling` | invoke `host.transport.post` with `body = "Day {{ world.day }} — traded 50 lbs of food to a {{ world.encounter_kind }} for one clothing set. A fair deal on the trail."`, `phase_id = "leg_a_event_encounter_traded_{{ world.day }}"`, `thread = "{{ run.id }}"`, `title = "Trade with a {{ world.encounter_kind }}"`, `transport = "tui"` · set `clothing_sets = "{{ world.clothing_sets + 1 }}"`, `current_event_attempts = 0`, `encounter_kind = ""`, `event_kind = ""`, `food_lbs = "{{ world.food_lbs - 50 }}"` · say "Trade complete: -50 lbs food, +1 clothing set." |
| 2 | [`accept_trade`](#intent-accept-trade) | _default_ | `.` | _hint: Not enough food to trade._ |
| 3 | [`decline_trade`](#intent-decline-trade) |  | `../traveling` | invoke `host.transport.post` with `body = "Day {{ world.day }} — passed on a trade with a {{ world.encounter_kind }}. The wagon kept rolling."`, `phase_id = "leg_a_event_encounter_declined_{{ world.day }}"`, `thread = "{{ run.id }}"`, `title = "Declined a trade"`, `transport = "tui"` · set `current_event_attempts = 0`, `encounter_kind = ""`, `event_kind = ""` · say "The party declines the trade and presses on." |
| 4 | [`look`](#intent-look) |  | `.` |  |
| 5 | [`move_on`](#intent-move-on) |  | `../traveling` | invoke `host.transport.post` with `body = "Day {{ world.day }} — moved on past the {{ world.encounter_kind }}. No words exchanged."`, `phase_id = "leg_a_event_encounter_moved_on_{{ world.day }}"`, `thread = "{{ run.id }}"`, `title = "Moved on from a {{ world.encounter_kind }}"`, `transport = "tui"` · set `current_event_attempts = 0`, `encounter_kind = ""`, `event_kind = ""` · say "The party moves on past the encounter." |
| 6 | [`quit`](#intent-quit) |  | [`ended_lost`](#room-ended-lost) | say "The party gives up at the encounter." |

### <a id="room-leg-a-executing-event-supply-loss"></a> `leg_a_executing.event_supply_loss`

Supplies lost on the trail.

**Shows world**: `food_lbs`, `oxen`, `rng_last`, `last_event_prose`

**On enter**:

1. set `current_event_attempts = 0`, `last_event = "{{ if world.rng_last % 2 == 0 }}food_loss{{ else }}ox_loss{{ end }}"`, `last_event_prose = ""`
2. set `food_lbs = "{{ world.rng_last % 2 == 0 ? world.food_lbs - (10 + 10 * (world.rng_last % 4)) : world.food_lbs }}"`, `oxen = "{{ world.rng_last % 2 == 0 ? world.oxen : world.oxen - 1 }}"`
3. invoke `host.agent.ask` with `agent = "trail_narrator"`, `args = map[current_landmark:{{ world.current_landmark }} day:{{ world.day }} food_lbs:{{ world.food_lbs }} oxen:{{ world.oxen }} what:{{ world.rng_last % 2 == 0 ? 'food spoiled' : 'ox lame' }}]`, `prompt_path = "prompts/event_supply_loss.md"`, bind `last_event_prose ← stdout`

**Transitions**:

| # | Intent | Guard | → | Effects |
|---|---|---|---|---|
| 1 | [`look`](#intent-look) |  | `.` |  |
| 2 | [`move_on`](#intent-move-on) |  | `../traveling` | invoke `host.transport.post` with `body = "Day {{ world.day }} — {{ world.last_event == 'food_loss' ? 'food spoiled' : 'an ox went lame' }} on the trail. Recovered what we could and pressed on. Food: {{ world.food_lbs }} lbs, oxen: {{ world.oxen }}."`, `phase_id = "leg_a_event_supply_loss_recovered_{{ world.day }}"`, `thread = "{{ run.id }}"`, `title = "Supplies lost"`, `transport = "tui"` · set `current_event_attempts = 0`, `event_kind = ""`, `last_event = ""` · say "The party recovers what it can and presses on." |
| 3 | [`quit`](#intent-quit) |  | [`ended_lost`](#room-ended-lost) | say "The party gives up after losing supplies." |

### <a id="room-leg-a-executing-event-weather"></a> `leg_a_executing.event_weather`

Severe weather: {{ world.weather_kind }}.

**Shows world**: `weather_kind`, `day`, `food_lbs`, `health_avg`, `last_event_prose`

**On enter**:

1. set `current_event_attempts = 0`, `last_event_prose = ""`, `weather_kind = "{{ world.month == 'november' || world.month == 'december' || world.month == 'january' || world.month == 'february' ? 'snow' : (world.month == 'march' || world.month == 'april' || world.month == 'may' ? 'heavy_rain' : (world.month == 'june' || world.month == 'july' || world.month == 'august' ? (world.rng_last % 2 == 0 ? 'hail' : 'fog') : (world.rng_last % 2 == 0 ? 'heavy_rain' : 'fog'))) }}"`
2. invoke `host.agent.ask` with `agent = "trail_narrator"`, `args = map[current_landmark:{{ world.current_landmark }} day:{{ world.day }} kind:{{ world.month == 'november' || world.month == 'december' || world.month == 'january' || world.month == 'february' ? 'snow' : (world.month == 'march' || world.month == 'april' || world.month == 'may' ? 'heavy_rain' : (world.month == 'june' || world.month == 'july' || world.month == 'august' ? (world.rng_last % 2 == 0 ? 'hail' : 'fog') : (world.rng_last % 2 == 0 ? 'heavy_rain' : 'fog'))) }} month:{{ world.month }} terrain:prairie]`, `prompt_path = "prompts/event_weather.md"`, bind `last_event_prose ← stdout`

**Transitions**:

| # | Intent | Guard | → | Effects |
|---|---|---|---|---|
| 1 | [`look`](#intent-look) |  | `.` |  |
| 2 | [`push_on`](#intent-push-on) |  | `../traveling` | invoke `host.transport.post` with `body = "Day {{ world.day }} — pushed on through the {{ world.weather_kind }}. Cold and wet; health worsened by 10."`, `phase_id = "leg_a_event_weather_pushed_on_{{ world.day }}"`, `thread = "{{ run.id }}"`, `title = "Pushed on through the weather"`, `transport = "tui"` · set `current_event_attempts = 0`, `event_kind = ""`, `health_avg = "{{ world.health_avg - 10 }}"`, `weather_kind = ""` · say "The party pushes on through the weather." |
| 3 | [`quit`](#intent-quit) |  | [`ended_lost`](#room-ended-lost) | say "The party gives up in the {{ world.weather_kind }}." |
| 4 | [`wait_out`](#intent-wait-out) |  | `../traveling` | invoke `host.transport.post` with `body = "Day {{ world.day }} — sheltered until the {{ world.weather_kind }} let up. {{ world.weather_kind == 'heavy_rain' ? '20 lbs of food spoiled in the wet.' : 'No supplies lost — only days.' }}"`, `phase_id = "leg_a_event_weather_waited_{{ world.day }}"`, `thread = "{{ run.id }}"`, `title = "Weather passed"`, `transport = "tui"` · set `current_event_attempts = 0`, `day = "{{ world.day + (world.rng_last % 3) + 1 }}"`, `event_kind = ""`, `food_lbs = "{{ world.weather_kind == 'heavy_rain' ? world.food_lbs - 20 : world.food_lbs }}"`, `weather_kind = ""` · say "The party shelters until the weather passes." |

### <a id="room-leg-a-executing-traveling"></a> `leg_a_executing.traveling`

Travelling — Independence → Kansas River Crossing.

**Transitions**:

| # | Intent | Guard | → | Effects |
|---|---|---|---|---|
| 1 | [`continue`](#intent-continue) | `world.miles_traveled + int((world.pace == 'grueling' ? 22 : (world.pace == 'strenuous' ? 18 : 14)) * (world.month == 'december' \|\| world.month == 'january' \|\| world.month == 'february' ? 70 : (world.month == 'november' ? 80 : (world.month == 'march' \|\| world.month == 'october' ? 85 : (world.month == 'april' \|\| world.month == 'september' ? 95 : 100)))) / 100) >= 102` | [`leg_a_awaiting_reply`](#room-leg-a-awaiting-reply) | set `day = "{{ world.day + 1 > 30 ? 1 : world.day + 1 }}"`, `food_lbs = "{{ world.food_lbs - (world.rations == 'bare_bones' ? 6 : (world.rations == 'meager' ? 8 : 10)) }}"`, `miles_traveled = "{{ world.miles_traveled + int((world.pace == 'grueling' ? 22 : (world.pace == 'strenuous' ? 18 : 14)) * (world.month == 'december' || world.month == 'january' || world.month == 'february' ? 70 : (world.month == 'november' ? 80 : (world.month == 'march' || world.month == 'october' ? 85 : (world.month == 'april' || world.month == 'september' ? 95 : 100)))) / 100) }}"`, `year = "{{ world.day + 1 > 30 && world.month == 'december' ? world.year + 1 : world.year }}"` · set `month = "{{ world.day + 1 > 30 ? (world.month == 'march' ? 'april' : (world.month == 'april' ? 'may' : (world.month == 'may' ? 'june' : (world.month == 'june' ? 'july' : (world.month == 'july' ? 'august' : (world.month == 'august' ? 'september' : (world.month == 'september' ? 'october' : (world.month == 'october' ? 'november' : (world.month == 'november' ? 'december' : (world.month == 'december' ? 'january' : (world.month == 'january' ? 'february' : (world.month == 'february' ? 'march' : world.month)))))))))))) : world.month }}"` · say "Arrived at Kansas River Crossing." |
| 2 | [`continue`](#intent-continue) | `world.food_lbs - (world.rations == 'bare_bones' ? 6 : (world.rations == 'meager' ? 8 : 10)) <= 0` | [`ended_lost`](#room-ended-lost) | set `day = "{{ world.day + 1 > 30 ? 1 : world.day + 1 }}"`, `food_lbs = 0`, `year = "{{ world.day + 1 > 30 && world.month == 'december' ? world.year + 1 : world.year }}"` · set `month = "{{ world.day + 1 > 30 ? (world.month == 'march' ? 'april' : (world.month == 'april' ? 'may' : (world.month == 'may' ? 'june' : (world.month == 'june' ? 'july' : (world.month == 'july' ? 'august' : (world.month == 'august' ? 'september' : (world.month == 'september' ? 'october' : (world.month == 'october' ? 'november' : (world.month == 'november' ? 'december' : (world.month == 'december' ? 'january' : (world.month == 'january' ? 'february' : (world.month == 'february' ? 'march' : world.month)))))))))))) : world.month }}"` · say "The party has run out of food between Independence and Kansas River Crossing. They starve on the trail." |
| 3 | [`continue`](#intent-continue) | `int(world.miles_traveled + world.rng_counter) % 100 >= 60 && int(world.miles_traveled + world.rng_counter) % 100 < 75` | `../event_disease` | set `rng_last = "{{ int(world.miles_traveled + world.rng_counter) % 100 }}"` · set `day = "{{ world.day + 1 > 30 ? 1 : world.day + 1 }}"`, `event_kind = "disease"`, `food_lbs = "{{ world.food_lbs - (world.rations == 'bare_bones' ? 6 : (world.rations == 'meager' ? 8 : 10)) }}"`, `miles_traveled = "{{ world.miles_traveled + int((world.pace == 'grueling' ? 22 : (world.pace == 'strenuous' ? 18 : 14)) * (world.month == 'december' || world.month == 'january' || world.month == 'february' ? 70 : (world.month == 'november' ? 80 : (world.month == 'march' || world.month == 'october' ? 85 : (world.month == 'april' || world.month == 'september' ? 95 : 100)))) / 100) }}"`, `year = "{{ world.day + 1 > 30 && world.month == 'december' ? world.year + 1 : world.year }}"` · set `month = "{{ world.day + 1 > 30 ? (world.month == 'march' ? 'april' : (world.month == 'april' ? 'may' : (world.month == 'may' ? 'june' : (world.month == 'june' ? 'july' : (world.month == 'july' ? 'august' : (world.month == 'august' ? 'september' : (world.month == 'september' ? 'october' : (world.month == 'october' ? 'november' : (world.month == 'november' ? 'december' : (world.month == 'december' ? 'january' : (world.month == 'january' ? 'february' : (world.month == 'february' ? 'march' : world.month)))))))))))) : world.month }}"` · increment `rng_counter += 1` |
| 4 | [`continue`](#intent-continue) | `int(world.miles_traveled + world.rng_counter) % 100 >= 75 && int(world.miles_traveled + world.rng_counter) % 100 < 85` | `../event_breakdown` | set `rng_last = "{{ int(world.miles_traveled + world.rng_counter) % 100 }}"` · set `day = "{{ world.day + 1 > 30 ? 1 : world.day + 1 }}"`, `event_kind = "breakdown"`, `food_lbs = "{{ world.food_lbs - (world.rations == 'bare_bones' ? 6 : (world.rations == 'meager' ? 8 : 10)) }}"`, `miles_traveled = "{{ world.miles_traveled + int((world.pace == 'grueling' ? 22 : (world.pace == 'strenuous' ? 18 : 14)) * (world.month == 'december' || world.month == 'january' || world.month == 'february' ? 70 : (world.month == 'november' ? 80 : (world.month == 'march' || world.month == 'october' ? 85 : (world.month == 'april' || world.month == 'september' ? 95 : 100)))) / 100) }}"`, `year = "{{ world.day + 1 > 30 && world.month == 'december' ? world.year + 1 : world.year }}"` · set `month = "{{ world.day + 1 > 30 ? (world.month == 'march' ? 'april' : (world.month == 'april' ? 'may' : (world.month == 'may' ? 'june' : (world.month == 'june' ? 'july' : (world.month == 'july' ? 'august' : (world.month == 'august' ? 'september' : (world.month == 'september' ? 'october' : (world.month == 'october' ? 'november' : (world.month == 'november' ? 'december' : (world.month == 'december' ? 'january' : (world.month == 'january' ? 'february' : (world.month == 'february' ? 'march' : world.month)))))))))))) : world.month }}"` · increment `rng_counter += 1` |
| 5 | [`continue`](#intent-continue) | `int(world.miles_traveled + world.rng_counter) % 100 >= 85 && int(world.miles_traveled + world.rng_counter) % 100 < 92` | `../event_weather` | set `rng_last = "{{ int(world.miles_traveled + world.rng_counter) % 100 }}"` · set `day = "{{ world.day + 1 > 30 ? 1 : world.day + 1 }}"`, `event_kind = "weather"`, `food_lbs = "{{ world.food_lbs - (world.rations == 'bare_bones' ? 6 : (world.rations == 'meager' ? 8 : 10)) }}"`, `miles_traveled = "{{ world.miles_traveled + int((world.pace == 'grueling' ? 22 : (world.pace == 'strenuous' ? 18 : 14)) * (world.month == 'december' || world.month == 'january' || world.month == 'february' ? 70 : (world.month == 'november' ? 80 : (world.month == 'march' || world.month == 'october' ? 85 : (world.month == 'april' || world.month == 'september' ? 95 : 100)))) / 100) }}"`, `year = "{{ world.day + 1 > 30 && world.month == 'december' ? world.year + 1 : world.year }}"` · set `month = "{{ world.day + 1 > 30 ? (world.month == 'march' ? 'april' : (world.month == 'april' ? 'may' : (world.month == 'may' ? 'june' : (world.month == 'june' ? 'july' : (world.month == 'july' ? 'august' : (world.month == 'august' ? 'september' : (world.month == 'september' ? 'october' : (world.month == 'october' ? 'november' : (world.month == 'november' ? 'december' : (world.month == 'december' ? 'january' : (world.month == 'january' ? 'february' : (world.month == 'february' ? 'march' : world.month)))))))))))) : world.month }}"` · increment `rng_counter += 1` |
| 6 | [`continue`](#intent-continue) | `int(world.miles_traveled + world.rng_counter) % 100 >= 92 && int(world.miles_traveled + world.rng_counter) % 100 < 97` | `../event_encounter` | set `rng_last = "{{ int(world.miles_traveled + world.rng_counter) % 100 }}"` · set `day = "{{ world.day + 1 > 30 ? 1 : world.day + 1 }}"`, `event_kind = "encounter"`, `food_lbs = "{{ world.food_lbs - (world.rations == 'bare_bones' ? 6 : (world.rations == 'meager' ? 8 : 10)) }}"`, `miles_traveled = "{{ world.miles_traveled + int((world.pace == 'grueling' ? 22 : (world.pace == 'strenuous' ? 18 : 14)) * (world.month == 'december' || world.month == 'january' || world.month == 'february' ? 70 : (world.month == 'november' ? 80 : (world.month == 'march' || world.month == 'october' ? 85 : (world.month == 'april' || world.month == 'september' ? 95 : 100)))) / 100) }}"`, `year = "{{ world.day + 1 > 30 && world.month == 'december' ? world.year + 1 : world.year }}"` · set `month = "{{ world.day + 1 > 30 ? (world.month == 'march' ? 'april' : (world.month == 'april' ? 'may' : (world.month == 'may' ? 'june' : (world.month == 'june' ? 'july' : (world.month == 'july' ? 'august' : (world.month == 'august' ? 'september' : (world.month == 'september' ? 'october' : (world.month == 'october' ? 'november' : (world.month == 'november' ? 'december' : (world.month == 'december' ? 'january' : (world.month == 'january' ? 'february' : (world.month == 'february' ? 'march' : world.month)))))))))))) : world.month }}"` · increment `rng_counter += 1` |
| 7 | [`continue`](#intent-continue) | `int(world.miles_traveled + world.rng_counter) % 100 >= 97` | `../event_supply_loss` | set `rng_last = "{{ int(world.miles_traveled + world.rng_counter) % 100 }}"` · set `day = "{{ world.day + 1 > 30 ? 1 : world.day + 1 }}"`, `event_kind = "supply_loss"`, `food_lbs = "{{ world.food_lbs - (world.rations == 'bare_bones' ? 6 : (world.rations == 'meager' ? 8 : 10)) }}"`, `miles_traveled = "{{ world.miles_traveled + int((world.pace == 'grueling' ? 22 : (world.pace == 'strenuous' ? 18 : 14)) * (world.month == 'december' || world.month == 'january' || world.month == 'february' ? 70 : (world.month == 'november' ? 80 : (world.month == 'march' || world.month == 'october' ? 85 : (world.month == 'april' || world.month == 'september' ? 95 : 100)))) / 100) }}"`, `year = "{{ world.day + 1 > 30 && world.month == 'december' ? world.year + 1 : world.year }}"` · set `month = "{{ world.day + 1 > 30 ? (world.month == 'march' ? 'april' : (world.month == 'april' ? 'may' : (world.month == 'may' ? 'june' : (world.month == 'june' ? 'july' : (world.month == 'july' ? 'august' : (world.month == 'august' ? 'september' : (world.month == 'september' ? 'october' : (world.month == 'october' ? 'november' : (world.month == 'november' ? 'december' : (world.month == 'december' ? 'january' : (world.month == 'january' ? 'february' : (world.month == 'february' ? 'march' : world.month)))))))))))) : world.month }}"` · increment `rng_counter += 1` |
| 8 | [`continue`](#intent-continue) | _default_ | `.` | set `rng_last = "{{ int(world.miles_traveled + world.rng_counter) % 100 }}"` · set `day = "{{ world.day + 1 > 30 ? 1 : world.day + 1 }}"`, `event_kind = ""`, `food_lbs = "{{ world.food_lbs - (world.rations == 'bare_bones' ? 6 : (world.rations == 'meager' ? 8 : 10)) }}"`, `miles_traveled = "{{ world.miles_traveled + int((world.pace == 'grueling' ? 22 : (world.pace == 'strenuous' ? 18 : 14)) * (world.month == 'december' || world.month == 'january' || world.month == 'february' ? 70 : (world.month == 'november' ? 80 : (world.month == 'march' || world.month == 'october' ? 85 : (world.month == 'april' || world.month == 'september' ? 95 : 100)))) / 100) }}"`, `year = "{{ world.day + 1 > 30 && world.month == 'december' ? world.year + 1 : world.year }}"` · set `month = "{{ world.day + 1 > 30 ? (world.month == 'march' ? 'april' : (world.month == 'april' ? 'may' : (world.month == 'may' ? 'june' : (world.month == 'june' ? 'july' : (world.month == 'july' ? 'august' : (world.month == 'august' ? 'september' : (world.month == 'september' ? 'october' : (world.month == 'october' ? 'november' : (world.month == 'november' ? 'december' : (world.month == 'december' ? 'january' : (world.month == 'january' ? 'february' : (world.month == 'february' ? 'march' : world.month)))))))))))) : world.month }}"` · increment `rng_counter += 1` |
| 9 | [`look`](#intent-look) |  | `.` |  |
| 10 | [`quit`](#intent-quit) |  | [`ended_lost`](#room-ended-lost) | say "The party abandons the journey between Independence and Kansas River Crossing." |
| 11 | [`set_pace`](#intent-set-pace) |  | `.` | set `pace = "{{ slots.pace }}"` · say "Pace set to {{ slots.pace }}." |
| 12 | [`set_rations`](#intent-set-rations) |  | `.` | set `rations = "{{ slots.rations }}"` · say "Rations set to {{ slots.rations }}." |

### <a id="room-leg-b-awaiting-reply"></a> `leg_b_awaiting_reply`

Arrived at Fort Kearney (prairie).

**Shows world**: `day`, `miles_traveled`, `food_lbs`, `oxen`, `party_alive`, `health_avg`, `illness_kind`, `illness_severity`, `illness_member`, `last_landmark_prose`

**On enter**:

1. set `last_landmark_prose = ""`
2. invoke `host.transport.post` with `body = "Day {{ world.day }}, {{ world.month }} {{ world.year }}. We rolled into **Fort Kearney** (prairie) at last.\n\n- Food: {{ world.food_lbs }} lbs\n- Oxen: {{ world.oxen }}\n- Party: {{ world.party_alive }} alive\n- Health: {{ world.health_avg }}\n"`, `phase_id = "leg_b_arrival"`, `thread = "{{ run.id }}"`, `title = "Day {{ world.day }}: Fort Kearney"`, `transport = "tui"`
3. invoke `host.agent.ask` with `agent = "trail_narrator"`, `args = map[day:{{ world.day }} food_lbs:{{ world.food_lbs }} landmark:Fort Kearney miles_traveled:{{ world.miles_traveled }} month:{{ world.month }} party_alive:{{ world.party_alive }} year:{{ world.year }}]`, `prompt_path = "prompts/landmark_arrival.md"`, bind `last_landmark_prose ← stdout`

**Transitions**:

| # | Intent | Guard | → | Effects |
|---|---|---|---|---|
| 1 | [`approach_river`](#intent-approach-river) | `false` | [`river_crossing`](#room-river-crossing) | set `current_landmark = "Fort Kearney"`, `river_depth_ft = "{{ int(0 * (world.month == 'april' ? 160 : (world.month == 'march' ? 140 : (world.month == 'may' ? 130 : (world.month == 'june' ? 100 : (world.month == 'july' ? 80 : (world.month == 'august' ? 70 : (world.month == 'september' ? 80 : 100))))))) / 100) }}"`, `river_width_ft = "{{ int(0) }}"` |
| 2 | [`approach_river`](#intent-approach-river) | _default_ | `.` | _hint: No river at this landmark._ |
| 3 | [`consult_guide`](#intent-consult-guide) |  | [`trail_guide`](#room-trail-guide) | set `last_job_originating_state = "leg_b_awaiting_reply"` · _(no-history)_ |
| 4 | [`continue`](#intent-continue) | `'Fort Kearney' == 'South Pass' && (world.month == 'october' \|\| world.month == 'november' \|\| world.month == 'december' \|\| world.month == 'january' \|\| world.month == 'february')` | [`snow_blocked`](#room-snow-blocked) | set `current_landmark = "Fort Kearney"` · say "South Pass is snowed in. The wagons cannot get through." |
| 5 | [`continue`](#intent-continue) | _default_ | [`leg_c_executing`](#room-leg-c-executing) | set `breakdown_part = ""`, `current_event_attempts = 0`, `current_landmark = "Fort Kearney"`, `encounter_kind = ""`, `event_kind = ""`, `illness_kind = ""`, `miles_traveled = 0`, `weather_kind = ""` |
| 6 | [`enter_fort`](#intent-enter-fort) | `true` | [`fort`](#room-fort) | set `current_landmark = "Fort Kearney"` |
| 7 | [`enter_fort`](#intent-enter-fort) | _default_ | `.` | _hint: No fort at this landmark._ |
| 8 | [`face_robbery`](#intent-face-robbery) |  | [`frontier`](#room-frontier) |  |
| 9 | [`give_up_leg`](#intent-give-up-leg) | `world.cycle__leg_b__on_failure < 2` | [`leg_a_executing`](#room-leg-a-executing) | increment `cycle__leg_b__on_failure += 1` · set `breakdown_part = ""`, `current_event_attempts = 0`, `encounter_kind = ""`, `event_kind = ""`, `illness_kind = ""`, `miles_traveled = 0`, `weather_kind = ""` · say "The party turns back toward Kansas River Crossing." |
| 10 | [`give_up_leg`](#intent-give-up-leg) | _default_ | [`leg_b_error`](#room-leg-b-error) | say "The party has given up too many times — stranded." |
| 11 | [`hunt`](#intent-hunt) |  | [`hunt`](#room-hunt) |  |
| 12 | [`quit`](#intent-quit) |  | [`ended_lost`](#room-ended-lost) | say "The party abandons the journey at Fort Kearney." |
| 13 | [`rest`](#intent-rest) |  | [`rest_room`](#room-rest-room) |  |
| 14 | [`restart_from`](#intent-restart-from) | `slots.stage == 'independence' \|\| slots.stage == 'kansas'` | [`leg_a_executing`](#room-leg-a-executing) | set `breakdown_part = ""`, `current_event_attempts = 0`, `encounter_kind = ""`, `event_kind = ""`, `illness_kind = ""`, `miles_traveled = 0`, `weather_kind = ""` · say "The party doubles back to Independence to retry the run toward Kansas River." |
| 15 | [`restart_from`](#intent-restart-from) | `slots.stage == 'kearney'` | [`leg_b_executing`](#room-leg-b-executing) | set `breakdown_part = ""`, `current_event_attempts = 0`, `encounter_kind = ""`, `event_kind = ""`, `illness_kind = ""`, `miles_traveled = 0`, `weather_kind = ""` · say "The party doubles back to Kansas River to retry the stretch toward Fort Kearney." |
| 16 | [`restart_from`](#intent-restart-from) | `slots.stage == 'chimney'` | [`leg_c_executing`](#room-leg-c-executing) | set `breakdown_part = ""`, `current_event_attempts = 0`, `encounter_kind = ""`, `event_kind = ""`, `illness_kind = ""`, `miles_traveled = 0`, `weather_kind = ""` · say "The party doubles back to Fort Kearney to retry the stretch toward Chimney Rock." |
| 17 | [`restart_from`](#intent-restart-from) | `slots.stage == 'laramie'` | [`leg_d_executing`](#room-leg-d-executing) | set `breakdown_part = ""`, `current_event_attempts = 0`, `encounter_kind = ""`, `event_kind = ""`, `illness_kind = ""`, `miles_traveled = 0`, `weather_kind = ""` · say "The party doubles back to Chimney Rock to retry the stretch toward Fort Laramie." |
| 18 | [`restart_from`](#intent-restart-from) | `slots.stage == 'south_pass'` | [`leg_e_executing`](#room-leg-e-executing) | set `breakdown_part = ""`, `current_event_attempts = 0`, `encounter_kind = ""`, `event_kind = ""`, `illness_kind = ""`, `miles_traveled = 0`, `weather_kind = ""` · say "The party doubles back to Fort Laramie to retry the stretch toward South Pass." |
| 19 | [`restart_from`](#intent-restart-from) | `slots.stage == 'snake'` | [`leg_f_executing`](#room-leg-f-executing) | set `breakdown_part = ""`, `current_event_attempts = 0`, `encounter_kind = ""`, `event_kind = ""`, `illness_kind = ""`, `miles_traveled = 0`, `weather_kind = ""` · say "The party doubles back to South Pass to retry the stretch toward Snake River." |
| 20 | [`restart_from`](#intent-restart-from) | _default_ | `.` | _hint: Unknown restart stage._ |
| 21 | [`scout`](#intent-scout) |  | [`frontier`](#room-frontier) |  |

**Timeout**: after `10d` → `leg_c_executing`

### <a id="room-leg-b-error"></a> `leg_b_error`

Stranded between Kansas River Crossing and Fort Kearney.

**Shows world**: `day`, `party_alive`, `current_landmark`

**Transitions**:

| # | Intent | Guard | → | Effects |
|---|---|---|---|---|
| 1 | [`quit`](#intent-quit) |  | [`ended_lost`](#room-ended-lost) | say "The party gives up between Kansas River Crossing and Fort Kearney." |
| 2 | [`retry`](#intent-retry) |  | [`leg_b_executing`](#room-leg-b-executing) | set `current_event_attempts = 0`, `event_kind = ""` |

### <a id="room-leg-b-executing"></a> `leg_b_executing`  _(compound)_

On the trail from Kansas River Crossing to Fort Kearney (prairie).

**Initial child**: `traveling`

**Shows world**: `day`, `month`, `miles_traveled`, `food_lbs`, `oxen`, `party_alive`, `health_avg`, `pace`, `rations`, `current_landmark`, `event_kind`, `illness_kind`, `illness_severity`, `illness_member`, `breakdown_part`, `weather_kind`, `encounter_kind`, `rng_last`, `rng_counter`

**On enter**:

1. set `current_landmark = "Kansas River Crossing"`

**Transitions**:

| # | Intent | Guard | → | Effects |
|---|---|---|---|---|
| 1 | [`on_failure`](#intent-on-failure) | `world.cycle__leg_b__on_failure < 2` | [`leg_a_executing`](#room-leg-a-executing) | increment `cycle__leg_b__on_failure += 1` |
| 2 | [`on_failure`](#intent-on-failure) | _default_ | [`leg_b_error`](#room-leg-b-error) | _hint: cycle budget exceeded for on_failure_ |

### <a id="room-leg-b-executing-event-breakdown"></a> `leg_b_executing.event_breakdown`

Wagon breakdown — {{ world.breakdown_part }}.

**Shows world**: `breakdown_part`, `spare_wheels`, `spare_axles`, `spare_tongues`, `day`, `current_event_attempts`, `last_event_prose`

**On enter**:

1. set `breakdown_part = "{{ if world.rng_last % 3 == 0 }}wheel{{ else }}{{ if world.rng_last % 3 == 1 }}axle{{ else }}tongue{{ end }}{{ end }}"`, `current_event_attempts = 0`, `last_event_prose = ""`
2. invoke `host.agent.ask` with `agent = "trail_narrator"`, `args = map[current_landmark:{{ world.current_landmark }} day:{{ world.day }} part:{{ if world.rng_last % 3 == 0 }}wheel{{ else }}{{ if world.rng_last % 3 == 1 }}axle{{ else }}tongue{{ end }}{{ end }} spares_remaining:{{ world.rng_last % 3 == 0 ? world.spare_wheels : (world.rng_last % 3 == 1 ? world.spare_axles : world.spare_tongues) }}]`, `prompt_path = "prompts/event_breakdown.md"`, bind `last_event_prose ← stdout`

**Transitions**:

| # | Intent | Guard | → | Effects |
|---|---|---|---|---|
| 1 | [`look`](#intent-look) |  | `.` |  |
| 2 | [`quit`](#intent-quit) |  | [`ended_lost`](#room-ended-lost) | say "The party abandons the broken wagon." |
| 3 | [`repair`](#intent-repair) | `world.breakdown_part == 'wheel' && world.spare_wheels >= 1` | `../traveling` | invoke `host.transport.post` with `body = "Day {{ world.day }} — fitted the spare wheel and got the wagon rolling again. One less in reserve."`, `phase_id = "leg_b_event_breakdown_wheel_repaired_{{ world.day }}"`, `thread = "{{ run.id }}"`, `title = "Wheel repaired"`, `transport = "tui"` · set `breakdown_part = ""`, `current_event_attempts = 0`, `event_kind = ""`, `spare_wheels = "{{ world.spare_wheels - 1 }}"` · say "Spare wheel installed. Back on the trail." |
| 4 | [`repair`](#intent-repair) | `world.breakdown_part == 'axle' && world.spare_axles >= 1` | `../traveling` | invoke `host.transport.post` with `body = "Day {{ world.day }} — swapped in the spare axle. Took the morning, but the wagon's true again."`, `phase_id = "leg_b_event_breakdown_axle_repaired_{{ world.day }}"`, `thread = "{{ run.id }}"`, `title = "Axle repaired"`, `transport = "tui"` · set `breakdown_part = ""`, `current_event_attempts = 0`, `event_kind = ""`, `spare_axles = "{{ world.spare_axles - 1 }}"` · say "Spare axle installed. Back on the trail." |
| 5 | [`repair`](#intent-repair) | `world.breakdown_part == 'tongue' && world.spare_tongues >= 1` | `../traveling` | invoke `host.transport.post` with `body = "Day {{ world.day }} — pinned in the spare tongue and we hitched the oxen back up."`, `phase_id = "leg_b_event_breakdown_tongue_repaired_{{ world.day }}"`, `thread = "{{ run.id }}"`, `title = "Tongue repaired"`, `transport = "tui"` · set `breakdown_part = ""`, `current_event_attempts = 0`, `event_kind = ""`, `spare_tongues = "{{ world.spare_tongues - 1 }}"` · say "Spare tongue installed. Back on the trail." |
| 6 | [`repair`](#intent-repair) | `world.current_event_attempts < 2` | `.` | _hint: Need a spare {{ world.breakdown_part }} to repair._ · increment `current_event_attempts += 1` · say "No spare {{ world.breakdown_part }} on hand. Try repair again, wait_out, or look." |
| 7 | [`repair`](#intent-repair) | _default_ | `../traveling` | invoke `host.transport.post` with `body = "Day {{ world.day }} — couldn't repair the {{ world.breakdown_part }}. Lashed it together and pressed on at a cost: a member of the party was left behind."`, `phase_id = "leg_b_event_breakdown_failed_{{ world.day }}"`, `thread = "{{ run.id }}"`, `title = "Wagon limping on"`, `transport = "tui"` · set `breakdown_part = ""`, `current_event_attempts = 0`, `event_kind = ""`, `health_avg = "{{ world.health_avg - 20 }}"`, `party_alive = "{{ world.party_alive - 1 }}"` · say "Repeated repair attempts failed; the wagon is patched poorly and the party limps on. A member is left behind." |
| 8 | [`wait_out`](#intent-wait-out) |  | `../traveling` | invoke `host.transport.post` with `body = "Day {{ world.day }} — made camp and improvised a {{ world.breakdown_part }} repair over five days. Slow going, but back on the trail."`, `phase_id = "leg_b_event_breakdown_waited_{{ world.day }}"`, `thread = "{{ run.id }}"`, `title = "Improvised repair"`, `transport = "tui"` · set `breakdown_part = ""`, `current_event_attempts = 0`, `day = "{{ world.day + 5 }}"`, `event_kind = ""` · say "The party makes camp and improvises a repair over five days." |

### <a id="room-leg-b-executing-event-disease"></a> `leg_b_executing.event_disease`

Illness has struck the party ({{ world.illness_kind }}).

**Shows world**: `illness_kind`, `illness_severity`, `illness_treatment`, `illness_member`, `health_avg`, `party_alive`, `food_lbs`, `clothing_sets`, `day`, `current_event_attempts`

**On enter**:

1. set `current_event_attempts = 0`, `health_avg = "{{ world.health_avg - 10 }}"`, `illness_member = "{{ split(world.party_names, ',')[world.rng_last % world.party_alive] }}"`
2. set `illness_kind = "{{ if world.rng_last % 5 == 0 }}dysentery{{ else }}{{ if world.rng_last % 5 == 1 }}cholera{{ else }}{{ if world.rng_last % 5 == 2 }}typhoid{{ else }}{{ if world.rng_last % 5 == 3 }}measles{{ else }}exhaustion{{ end }}{{ end }}{{ end }}{{ end }}"`, `illness_severity = "{{ world.rng_last % 5 + 1 }}"`, `illness_treatment = "rest"`
3. invoke `host.agent.decide` with `agent = "frontier_doctor"`, `args = map[clothing_sets:{{ world.clothing_sets }} current_landmark:{{ world.current_landmark }} food_lbs:{{ world.food_lbs }} health_avg:{{ world.health_avg }} party_alive:{{ world.party_alive }} rng_last:{{ world.rng_last }}]`, `prompt = "prompts/event_disease.md"`, `schema = "mcp/illness.json"`, bind `illness_kind ← submitted.illness`, `illness_severity ← submitted.severity`, `illness_treatment ← submitted.treatment`, on_error → `leg_b_error`

**Transitions**:

| # | Intent | Guard | → | Effects |
|---|---|---|---|---|
| 1 | [`look`](#intent-look) |  | `.` |  |
| 2 | [`move_on`](#intent-move-on) |  | `../traveling` | invoke `host.transport.post` with `body = "Day {{ world.day }} — pushed on through the {{ world.illness_kind }} rather than stop. Health is worse for it, but the wagon rolls."`, `phase_id = "leg_b_event_disease_moved_on_{{ world.day }}"`, `thread = "{{ run.id }}"`, `title = "Pressing on through illness"`, `transport = "tui"` · set `current_event_attempts = 0`, `event_kind = ""`, `health_avg = "{{ world.health_avg - 15 }}"`, `illness_kind = ""`, `illness_member = ""`, `illness_severity = 0`, `illness_treatment = ""` · say "The party presses on. Health worsens." |
| 3 | [`quit`](#intent-quit) |  | [`ended_lost`](#room-ended-lost) | say "The party gives up on the trail, broken by {{ world.illness_kind }}." |
| 4 | [`treat`](#intent-treat) | `world.clothing_sets >= 1 && world.food_lbs >= 50 && world.current_event_attempts < 2` | `../traveling` | invoke `host.transport.post` with `body = "Day {{ world.day }} — the {{ world.illness_kind }} has passed. Rested up, one clothing set used and 50 lbs of food spent on broth and care. Spirits steady, on the move."`, `phase_id = "leg_b_event_disease_treated_{{ world.day }}"`, `thread = "{{ run.id }}"`, `title = "Disease treated"`, `transport = "tui"` · set `clothing_sets = "{{ world.clothing_sets - 1 }}"`, `current_event_attempts = 0`, `event_kind = ""`, `food_lbs = "{{ world.food_lbs - 50 }}"`, `health_avg = "{{ world.health_avg + 20 }}"`, `illness_kind = ""`, `illness_member = ""`, `illness_severity = 0`, `illness_treatment = ""` · say "The party rests and treats the illness." |
| 5 | [`treat`](#intent-treat) | `(world.clothing_sets < 1 \|\| world.food_lbs < 50) && world.current_event_attempts < 2` | `.` | increment `current_event_attempts += 1` · say "Not enough supplies to treat the {{ world.illness_kind }} (need 1 clothing set + 50 lbs food). Try again or wait_out." |
| 6 | [`treat`](#intent-treat) | _default_ | `../traveling` | invoke `host.transport.post` with `body = "Day {{ world.day }} — after repeated attempts, the {{ world.illness_kind }} took one of the party. May the trail remember them."`, `phase_id = "leg_b_event_disease_lost_{{ world.day }}"`, `thread = "{{ run.id }}"`, `title = "Lost to illness"`, `transport = "tui"` · set `current_event_attempts = 0`, `event_kind = ""`, `illness_kind = ""`, `illness_member = ""`, `illness_severity = 0`, `illness_treatment = ""`, `party_alive = "{{ world.party_alive - 1 }}"` · say "After repeated futile attempts, a party member has died of illness." |
| 7 | [`wait_out`](#intent-wait-out) | `world.health_avg < 30` | `../traveling` | invoke `host.transport.post` with `body = "Day {{ world.day }} — the patient was too weak. {{ world.illness_kind }} claimed one of the party while we waited."`, `phase_id = "leg_b_event_disease_wait_lost_{{ world.day }}"`, `thread = "{{ run.id }}"`, `title = "Lost to illness"`, `transport = "tui"` · set `current_event_attempts = 0`, `day = "{{ world.day + 1 }}"`, `event_kind = ""`, `illness_kind = ""`, `illness_member = ""`, `illness_severity = 0`, `illness_treatment = ""`, `party_alive = "{{ world.party_alive - 1 }}"` · say "The patient was too weak. A party member dies of illness." |
| 8 | [`wait_out`](#intent-wait-out) | _default_ | `../traveling` | invoke `host.transport.post` with `body = "Day {{ world.day }} — one day of rest and the {{ world.illness_kind }} let go. We move on tomorrow."`, `phase_id = "leg_b_event_disease_wait_ok_{{ world.day }}"`, `thread = "{{ run.id }}"`, `title = "Illness passed"`, `transport = "tui"` · set `current_event_attempts = 0`, `day = "{{ world.day + 1 }}"`, `event_kind = ""`, `illness_kind = ""`, `illness_member = ""`, `illness_severity = 0`, `illness_treatment = ""` · say "The party rests a day. The illness passes." |

### <a id="room-leg-b-executing-event-encounter"></a> `leg_b_executing.event_encounter`

Encounter on the trail: {{ world.encounter_kind }}.

**Shows world**: `encounter_kind`, `food_lbs`, `clothing_sets`, `last_event_prose`

**On enter**:

1. set `current_event_attempts = 0`, `encounter_kind = "{{ if world.rng_last % 3 == 0 }}trader{{ else }}{{ if world.rng_last % 3 == 1 }}hunter{{ else }}band{{ end }}{{ end }}"`, `last_event_prose = ""`
2. invoke `host.agent.ask` with `agent = "trail_narrator"`, `args = map[clothing_sets:{{ world.clothing_sets }} current_landmark:{{ world.current_landmark }} day:{{ world.day }} food_lbs:{{ world.food_lbs }} kind:{{ if world.rng_last % 3 == 0 }}trader{{ else }}{{ if world.rng_last % 3 == 1 }}hunter{{ else }}band{{ end }}{{ end }}]`, `prompt_path = "prompts/event_encounter.md"`, bind `last_event_prose ← stdout`

**Transitions**:

| # | Intent | Guard | → | Effects |
|---|---|---|---|---|
| 1 | [`accept_trade`](#intent-accept-trade) | `world.food_lbs >= 50` | `../traveling` | invoke `host.transport.post` with `body = "Day {{ world.day }} — traded 50 lbs of food to a {{ world.encounter_kind }} for one clothing set. A fair deal on the trail."`, `phase_id = "leg_b_event_encounter_traded_{{ world.day }}"`, `thread = "{{ run.id }}"`, `title = "Trade with a {{ world.encounter_kind }}"`, `transport = "tui"` · set `clothing_sets = "{{ world.clothing_sets + 1 }}"`, `current_event_attempts = 0`, `encounter_kind = ""`, `event_kind = ""`, `food_lbs = "{{ world.food_lbs - 50 }}"` · say "Trade complete: -50 lbs food, +1 clothing set." |
| 2 | [`accept_trade`](#intent-accept-trade) | _default_ | `.` | _hint: Not enough food to trade._ |
| 3 | [`decline_trade`](#intent-decline-trade) |  | `../traveling` | invoke `host.transport.post` with `body = "Day {{ world.day }} — passed on a trade with a {{ world.encounter_kind }}. The wagon kept rolling."`, `phase_id = "leg_b_event_encounter_declined_{{ world.day }}"`, `thread = "{{ run.id }}"`, `title = "Declined a trade"`, `transport = "tui"` · set `current_event_attempts = 0`, `encounter_kind = ""`, `event_kind = ""` · say "The party declines the trade and presses on." |
| 4 | [`look`](#intent-look) |  | `.` |  |
| 5 | [`move_on`](#intent-move-on) |  | `../traveling` | invoke `host.transport.post` with `body = "Day {{ world.day }} — moved on past the {{ world.encounter_kind }}. No words exchanged."`, `phase_id = "leg_b_event_encounter_moved_on_{{ world.day }}"`, `thread = "{{ run.id }}"`, `title = "Moved on from a {{ world.encounter_kind }}"`, `transport = "tui"` · set `current_event_attempts = 0`, `encounter_kind = ""`, `event_kind = ""` · say "The party moves on past the encounter." |
| 6 | [`quit`](#intent-quit) |  | [`ended_lost`](#room-ended-lost) | say "The party gives up at the encounter." |

### <a id="room-leg-b-executing-event-supply-loss"></a> `leg_b_executing.event_supply_loss`

Supplies lost on the trail.

**Shows world**: `food_lbs`, `oxen`, `rng_last`, `last_event_prose`

**On enter**:

1. set `current_event_attempts = 0`, `last_event = "{{ if world.rng_last % 2 == 0 }}food_loss{{ else }}ox_loss{{ end }}"`, `last_event_prose = ""`
2. set `food_lbs = "{{ world.rng_last % 2 == 0 ? world.food_lbs - (10 + 10 * (world.rng_last % 4)) : world.food_lbs }}"`, `oxen = "{{ world.rng_last % 2 == 0 ? world.oxen : world.oxen - 1 }}"`
3. invoke `host.agent.ask` with `agent = "trail_narrator"`, `args = map[current_landmark:{{ world.current_landmark }} day:{{ world.day }} food_lbs:{{ world.food_lbs }} oxen:{{ world.oxen }} what:{{ world.rng_last % 2 == 0 ? 'food spoiled' : 'ox lame' }}]`, `prompt_path = "prompts/event_supply_loss.md"`, bind `last_event_prose ← stdout`

**Transitions**:

| # | Intent | Guard | → | Effects |
|---|---|---|---|---|
| 1 | [`look`](#intent-look) |  | `.` |  |
| 2 | [`move_on`](#intent-move-on) |  | `../traveling` | invoke `host.transport.post` with `body = "Day {{ world.day }} — {{ world.last_event == 'food_loss' ? 'food spoiled' : 'an ox went lame' }} on the trail. Recovered what we could and pressed on. Food: {{ world.food_lbs }} lbs, oxen: {{ world.oxen }}."`, `phase_id = "leg_b_event_supply_loss_recovered_{{ world.day }}"`, `thread = "{{ run.id }}"`, `title = "Supplies lost"`, `transport = "tui"` · set `current_event_attempts = 0`, `event_kind = ""`, `last_event = ""` · say "The party recovers what it can and presses on." |
| 3 | [`quit`](#intent-quit) |  | [`ended_lost`](#room-ended-lost) | say "The party gives up after losing supplies." |

### <a id="room-leg-b-executing-event-weather"></a> `leg_b_executing.event_weather`

Severe weather: {{ world.weather_kind }}.

**Shows world**: `weather_kind`, `day`, `food_lbs`, `health_avg`, `last_event_prose`

**On enter**:

1. set `current_event_attempts = 0`, `last_event_prose = ""`, `weather_kind = "{{ world.month == 'november' || world.month == 'december' || world.month == 'january' || world.month == 'february' ? 'snow' : (world.month == 'march' || world.month == 'april' || world.month == 'may' ? 'heavy_rain' : (world.month == 'june' || world.month == 'july' || world.month == 'august' ? (world.rng_last % 2 == 0 ? 'hail' : 'fog') : (world.rng_last % 2 == 0 ? 'heavy_rain' : 'fog'))) }}"`
2. invoke `host.agent.ask` with `agent = "trail_narrator"`, `args = map[current_landmark:{{ world.current_landmark }} day:{{ world.day }} kind:{{ world.month == 'november' || world.month == 'december' || world.month == 'january' || world.month == 'february' ? 'snow' : (world.month == 'march' || world.month == 'april' || world.month == 'may' ? 'heavy_rain' : (world.month == 'june' || world.month == 'july' || world.month == 'august' ? (world.rng_last % 2 == 0 ? 'hail' : 'fog') : (world.rng_last % 2 == 0 ? 'heavy_rain' : 'fog'))) }} month:{{ world.month }} terrain:prairie]`, `prompt_path = "prompts/event_weather.md"`, bind `last_event_prose ← stdout`

**Transitions**:

| # | Intent | Guard | → | Effects |
|---|---|---|---|---|
| 1 | [`look`](#intent-look) |  | `.` |  |
| 2 | [`push_on`](#intent-push-on) |  | `../traveling` | invoke `host.transport.post` with `body = "Day {{ world.day }} — pushed on through the {{ world.weather_kind }}. Cold and wet; health worsened by 10."`, `phase_id = "leg_b_event_weather_pushed_on_{{ world.day }}"`, `thread = "{{ run.id }}"`, `title = "Pushed on through the weather"`, `transport = "tui"` · set `current_event_attempts = 0`, `event_kind = ""`, `health_avg = "{{ world.health_avg - 10 }}"`, `weather_kind = ""` · say "The party pushes on through the weather." |
| 3 | [`quit`](#intent-quit) |  | [`ended_lost`](#room-ended-lost) | say "The party gives up in the {{ world.weather_kind }}." |
| 4 | [`wait_out`](#intent-wait-out) |  | `../traveling` | invoke `host.transport.post` with `body = "Day {{ world.day }} — sheltered until the {{ world.weather_kind }} let up. {{ world.weather_kind == 'heavy_rain' ? '20 lbs of food spoiled in the wet.' : 'No supplies lost — only days.' }}"`, `phase_id = "leg_b_event_weather_waited_{{ world.day }}"`, `thread = "{{ run.id }}"`, `title = "Weather passed"`, `transport = "tui"` · set `current_event_attempts = 0`, `day = "{{ world.day + (world.rng_last % 3) + 1 }}"`, `event_kind = ""`, `food_lbs = "{{ world.weather_kind == 'heavy_rain' ? world.food_lbs - 20 : world.food_lbs }}"`, `weather_kind = ""` · say "The party shelters until the weather passes." |

### <a id="room-leg-b-executing-traveling"></a> `leg_b_executing.traveling`

Travelling — Kansas River Crossing → Fort Kearney.

**Transitions**:

| # | Intent | Guard | → | Effects |
|---|---|---|---|---|
| 1 | [`continue`](#intent-continue) | `world.miles_traveled + int((world.pace == 'grueling' ? 22 : (world.pace == 'strenuous' ? 18 : 14)) * (world.month == 'december' \|\| world.month == 'january' \|\| world.month == 'february' ? 70 : (world.month == 'november' ? 80 : (world.month == 'march' \|\| world.month == 'october' ? 85 : (world.month == 'april' \|\| world.month == 'september' ? 95 : 100)))) / 100) >= 202` | [`leg_b_awaiting_reply`](#room-leg-b-awaiting-reply) | set `day = "{{ world.day + 1 > 30 ? 1 : world.day + 1 }}"`, `food_lbs = "{{ world.food_lbs - (world.rations == 'bare_bones' ? 6 : (world.rations == 'meager' ? 8 : 10)) }}"`, `miles_traveled = "{{ world.miles_traveled + int((world.pace == 'grueling' ? 22 : (world.pace == 'strenuous' ? 18 : 14)) * (world.month == 'december' || world.month == 'january' || world.month == 'february' ? 70 : (world.month == 'november' ? 80 : (world.month == 'march' || world.month == 'october' ? 85 : (world.month == 'april' || world.month == 'september' ? 95 : 100)))) / 100) }}"`, `year = "{{ world.day + 1 > 30 && world.month == 'december' ? world.year + 1 : world.year }}"` · set `month = "{{ world.day + 1 > 30 ? (world.month == 'march' ? 'april' : (world.month == 'april' ? 'may' : (world.month == 'may' ? 'june' : (world.month == 'june' ? 'july' : (world.month == 'july' ? 'august' : (world.month == 'august' ? 'september' : (world.month == 'september' ? 'october' : (world.month == 'october' ? 'november' : (world.month == 'november' ? 'december' : (world.month == 'december' ? 'january' : (world.month == 'january' ? 'february' : (world.month == 'february' ? 'march' : world.month)))))))))))) : world.month }}"` · say "Arrived at Fort Kearney." |
| 2 | [`continue`](#intent-continue) | `world.food_lbs - (world.rations == 'bare_bones' ? 6 : (world.rations == 'meager' ? 8 : 10)) <= 0` | [`ended_lost`](#room-ended-lost) | set `day = "{{ world.day + 1 > 30 ? 1 : world.day + 1 }}"`, `food_lbs = 0`, `year = "{{ world.day + 1 > 30 && world.month == 'december' ? world.year + 1 : world.year }}"` · set `month = "{{ world.day + 1 > 30 ? (world.month == 'march' ? 'april' : (world.month == 'april' ? 'may' : (world.month == 'may' ? 'june' : (world.month == 'june' ? 'july' : (world.month == 'july' ? 'august' : (world.month == 'august' ? 'september' : (world.month == 'september' ? 'october' : (world.month == 'october' ? 'november' : (world.month == 'november' ? 'december' : (world.month == 'december' ? 'january' : (world.month == 'january' ? 'february' : (world.month == 'february' ? 'march' : world.month)))))))))))) : world.month }}"` · say "The party has run out of food between Kansas River Crossing and Fort Kearney. They starve on the trail." |
| 3 | [`continue`](#intent-continue) | `int(world.miles_traveled + world.rng_counter) % 100 >= 60 && int(world.miles_traveled + world.rng_counter) % 100 < 75` | `../event_disease` | set `rng_last = "{{ int(world.miles_traveled + world.rng_counter) % 100 }}"` · set `day = "{{ world.day + 1 > 30 ? 1 : world.day + 1 }}"`, `event_kind = "disease"`, `food_lbs = "{{ world.food_lbs - (world.rations == 'bare_bones' ? 6 : (world.rations == 'meager' ? 8 : 10)) }}"`, `miles_traveled = "{{ world.miles_traveled + int((world.pace == 'grueling' ? 22 : (world.pace == 'strenuous' ? 18 : 14)) * (world.month == 'december' || world.month == 'january' || world.month == 'february' ? 70 : (world.month == 'november' ? 80 : (world.month == 'march' || world.month == 'october' ? 85 : (world.month == 'april' || world.month == 'september' ? 95 : 100)))) / 100) }}"`, `year = "{{ world.day + 1 > 30 && world.month == 'december' ? world.year + 1 : world.year }}"` · set `month = "{{ world.day + 1 > 30 ? (world.month == 'march' ? 'april' : (world.month == 'april' ? 'may' : (world.month == 'may' ? 'june' : (world.month == 'june' ? 'july' : (world.month == 'july' ? 'august' : (world.month == 'august' ? 'september' : (world.month == 'september' ? 'october' : (world.month == 'october' ? 'november' : (world.month == 'november' ? 'december' : (world.month == 'december' ? 'january' : (world.month == 'january' ? 'february' : (world.month == 'february' ? 'march' : world.month)))))))))))) : world.month }}"` · increment `rng_counter += 1` |
| 4 | [`continue`](#intent-continue) | `int(world.miles_traveled + world.rng_counter) % 100 >= 75 && int(world.miles_traveled + world.rng_counter) % 100 < 85` | `../event_breakdown` | set `rng_last = "{{ int(world.miles_traveled + world.rng_counter) % 100 }}"` · set `day = "{{ world.day + 1 > 30 ? 1 : world.day + 1 }}"`, `event_kind = "breakdown"`, `food_lbs = "{{ world.food_lbs - (world.rations == 'bare_bones' ? 6 : (world.rations == 'meager' ? 8 : 10)) }}"`, `miles_traveled = "{{ world.miles_traveled + int((world.pace == 'grueling' ? 22 : (world.pace == 'strenuous' ? 18 : 14)) * (world.month == 'december' || world.month == 'january' || world.month == 'february' ? 70 : (world.month == 'november' ? 80 : (world.month == 'march' || world.month == 'october' ? 85 : (world.month == 'april' || world.month == 'september' ? 95 : 100)))) / 100) }}"`, `year = "{{ world.day + 1 > 30 && world.month == 'december' ? world.year + 1 : world.year }}"` · set `month = "{{ world.day + 1 > 30 ? (world.month == 'march' ? 'april' : (world.month == 'april' ? 'may' : (world.month == 'may' ? 'june' : (world.month == 'june' ? 'july' : (world.month == 'july' ? 'august' : (world.month == 'august' ? 'september' : (world.month == 'september' ? 'october' : (world.month == 'october' ? 'november' : (world.month == 'november' ? 'december' : (world.month == 'december' ? 'january' : (world.month == 'january' ? 'february' : (world.month == 'february' ? 'march' : world.month)))))))))))) : world.month }}"` · increment `rng_counter += 1` |
| 5 | [`continue`](#intent-continue) | `int(world.miles_traveled + world.rng_counter) % 100 >= 85 && int(world.miles_traveled + world.rng_counter) % 100 < 92` | `../event_weather` | set `rng_last = "{{ int(world.miles_traveled + world.rng_counter) % 100 }}"` · set `day = "{{ world.day + 1 > 30 ? 1 : world.day + 1 }}"`, `event_kind = "weather"`, `food_lbs = "{{ world.food_lbs - (world.rations == 'bare_bones' ? 6 : (world.rations == 'meager' ? 8 : 10)) }}"`, `miles_traveled = "{{ world.miles_traveled + int((world.pace == 'grueling' ? 22 : (world.pace == 'strenuous' ? 18 : 14)) * (world.month == 'december' || world.month == 'january' || world.month == 'february' ? 70 : (world.month == 'november' ? 80 : (world.month == 'march' || world.month == 'october' ? 85 : (world.month == 'april' || world.month == 'september' ? 95 : 100)))) / 100) }}"`, `year = "{{ world.day + 1 > 30 && world.month == 'december' ? world.year + 1 : world.year }}"` · set `month = "{{ world.day + 1 > 30 ? (world.month == 'march' ? 'april' : (world.month == 'april' ? 'may' : (world.month == 'may' ? 'june' : (world.month == 'june' ? 'july' : (world.month == 'july' ? 'august' : (world.month == 'august' ? 'september' : (world.month == 'september' ? 'october' : (world.month == 'october' ? 'november' : (world.month == 'november' ? 'december' : (world.month == 'december' ? 'january' : (world.month == 'january' ? 'february' : (world.month == 'february' ? 'march' : world.month)))))))))))) : world.month }}"` · increment `rng_counter += 1` |
| 6 | [`continue`](#intent-continue) | `int(world.miles_traveled + world.rng_counter) % 100 >= 92 && int(world.miles_traveled + world.rng_counter) % 100 < 97` | `../event_encounter` | set `rng_last = "{{ int(world.miles_traveled + world.rng_counter) % 100 }}"` · set `day = "{{ world.day + 1 > 30 ? 1 : world.day + 1 }}"`, `event_kind = "encounter"`, `food_lbs = "{{ world.food_lbs - (world.rations == 'bare_bones' ? 6 : (world.rations == 'meager' ? 8 : 10)) }}"`, `miles_traveled = "{{ world.miles_traveled + int((world.pace == 'grueling' ? 22 : (world.pace == 'strenuous' ? 18 : 14)) * (world.month == 'december' || world.month == 'january' || world.month == 'february' ? 70 : (world.month == 'november' ? 80 : (world.month == 'march' || world.month == 'october' ? 85 : (world.month == 'april' || world.month == 'september' ? 95 : 100)))) / 100) }}"`, `year = "{{ world.day + 1 > 30 && world.month == 'december' ? world.year + 1 : world.year }}"` · set `month = "{{ world.day + 1 > 30 ? (world.month == 'march' ? 'april' : (world.month == 'april' ? 'may' : (world.month == 'may' ? 'june' : (world.month == 'june' ? 'july' : (world.month == 'july' ? 'august' : (world.month == 'august' ? 'september' : (world.month == 'september' ? 'october' : (world.month == 'october' ? 'november' : (world.month == 'november' ? 'december' : (world.month == 'december' ? 'january' : (world.month == 'january' ? 'february' : (world.month == 'february' ? 'march' : world.month)))))))))))) : world.month }}"` · increment `rng_counter += 1` |
| 7 | [`continue`](#intent-continue) | `int(world.miles_traveled + world.rng_counter) % 100 >= 97` | `../event_supply_loss` | set `rng_last = "{{ int(world.miles_traveled + world.rng_counter) % 100 }}"` · set `day = "{{ world.day + 1 > 30 ? 1 : world.day + 1 }}"`, `event_kind = "supply_loss"`, `food_lbs = "{{ world.food_lbs - (world.rations == 'bare_bones' ? 6 : (world.rations == 'meager' ? 8 : 10)) }}"`, `miles_traveled = "{{ world.miles_traveled + int((world.pace == 'grueling' ? 22 : (world.pace == 'strenuous' ? 18 : 14)) * (world.month == 'december' || world.month == 'january' || world.month == 'february' ? 70 : (world.month == 'november' ? 80 : (world.month == 'march' || world.month == 'october' ? 85 : (world.month == 'april' || world.month == 'september' ? 95 : 100)))) / 100) }}"`, `year = "{{ world.day + 1 > 30 && world.month == 'december' ? world.year + 1 : world.year }}"` · set `month = "{{ world.day + 1 > 30 ? (world.month == 'march' ? 'april' : (world.month == 'april' ? 'may' : (world.month == 'may' ? 'june' : (world.month == 'june' ? 'july' : (world.month == 'july' ? 'august' : (world.month == 'august' ? 'september' : (world.month == 'september' ? 'october' : (world.month == 'october' ? 'november' : (world.month == 'november' ? 'december' : (world.month == 'december' ? 'january' : (world.month == 'january' ? 'february' : (world.month == 'february' ? 'march' : world.month)))))))))))) : world.month }}"` · increment `rng_counter += 1` |
| 8 | [`continue`](#intent-continue) | _default_ | `.` | set `rng_last = "{{ int(world.miles_traveled + world.rng_counter) % 100 }}"` · set `day = "{{ world.day + 1 > 30 ? 1 : world.day + 1 }}"`, `event_kind = ""`, `food_lbs = "{{ world.food_lbs - (world.rations == 'bare_bones' ? 6 : (world.rations == 'meager' ? 8 : 10)) }}"`, `miles_traveled = "{{ world.miles_traveled + int((world.pace == 'grueling' ? 22 : (world.pace == 'strenuous' ? 18 : 14)) * (world.month == 'december' || world.month == 'january' || world.month == 'february' ? 70 : (world.month == 'november' ? 80 : (world.month == 'march' || world.month == 'october' ? 85 : (world.month == 'april' || world.month == 'september' ? 95 : 100)))) / 100) }}"`, `year = "{{ world.day + 1 > 30 && world.month == 'december' ? world.year + 1 : world.year }}"` · set `month = "{{ world.day + 1 > 30 ? (world.month == 'march' ? 'april' : (world.month == 'april' ? 'may' : (world.month == 'may' ? 'june' : (world.month == 'june' ? 'july' : (world.month == 'july' ? 'august' : (world.month == 'august' ? 'september' : (world.month == 'september' ? 'october' : (world.month == 'october' ? 'november' : (world.month == 'november' ? 'december' : (world.month == 'december' ? 'january' : (world.month == 'january' ? 'february' : (world.month == 'february' ? 'march' : world.month)))))))))))) : world.month }}"` · increment `rng_counter += 1` |
| 9 | [`look`](#intent-look) |  | `.` |  |
| 10 | [`quit`](#intent-quit) |  | [`ended_lost`](#room-ended-lost) | say "The party abandons the journey between Kansas River Crossing and Fort Kearney." |
| 11 | [`set_pace`](#intent-set-pace) |  | `.` | set `pace = "{{ slots.pace }}"` · say "Pace set to {{ slots.pace }}." |
| 12 | [`set_rations`](#intent-set-rations) |  | `.` | set `rations = "{{ slots.rations }}"` · say "Rations set to {{ slots.rations }}." |

### <a id="room-leg-c-awaiting-reply"></a> `leg_c_awaiting_reply`

Arrived at Chimney Rock (prairie).

**Shows world**: `day`, `miles_traveled`, `food_lbs`, `oxen`, `party_alive`, `health_avg`, `illness_kind`, `illness_severity`, `illness_member`, `last_landmark_prose`

**On enter**:

1. set `last_landmark_prose = ""`
2. invoke `host.transport.post` with `body = "Day {{ world.day }}, {{ world.month }} {{ world.year }}. We rolled into **Chimney Rock** (prairie) at last.\n\n- Food: {{ world.food_lbs }} lbs\n- Oxen: {{ world.oxen }}\n- Party: {{ world.party_alive }} alive\n- Health: {{ world.health_avg }}\n"`, `phase_id = "leg_c_arrival"`, `thread = "{{ run.id }}"`, `title = "Day {{ world.day }}: Chimney Rock"`, `transport = "tui"`
3. invoke `host.agent.ask` with `agent = "trail_narrator"`, `args = map[day:{{ world.day }} food_lbs:{{ world.food_lbs }} landmark:Chimney Rock miles_traveled:{{ world.miles_traveled }} month:{{ world.month }} party_alive:{{ world.party_alive }} year:{{ world.year }}]`, `prompt_path = "prompts/landmark_arrival.md"`, bind `last_landmark_prose ← stdout`

**Transitions**:

| # | Intent | Guard | → | Effects |
|---|---|---|---|---|
| 1 | [`approach_river`](#intent-approach-river) | `false` | [`river_crossing`](#room-river-crossing) | set `current_landmark = "Chimney Rock"`, `river_depth_ft = "{{ int(0 * (world.month == 'april' ? 160 : (world.month == 'march' ? 140 : (world.month == 'may' ? 130 : (world.month == 'june' ? 100 : (world.month == 'july' ? 80 : (world.month == 'august' ? 70 : (world.month == 'september' ? 80 : 100))))))) / 100) }}"`, `river_width_ft = "{{ int(0) }}"` |
| 2 | [`approach_river`](#intent-approach-river) | _default_ | `.` | _hint: No river at this landmark._ |
| 3 | [`consult_guide`](#intent-consult-guide) |  | [`trail_guide`](#room-trail-guide) | set `last_job_originating_state = "leg_c_awaiting_reply"` · _(no-history)_ |
| 4 | [`continue`](#intent-continue) | `'Chimney Rock' == 'South Pass' && (world.month == 'october' \|\| world.month == 'november' \|\| world.month == 'december' \|\| world.month == 'january' \|\| world.month == 'february')` | [`snow_blocked`](#room-snow-blocked) | set `current_landmark = "Chimney Rock"` · say "South Pass is snowed in. The wagons cannot get through." |
| 5 | [`continue`](#intent-continue) | _default_ | [`leg_d_executing`](#room-leg-d-executing) | set `breakdown_part = ""`, `current_event_attempts = 0`, `current_landmark = "Chimney Rock"`, `encounter_kind = ""`, `event_kind = ""`, `illness_kind = ""`, `miles_traveled = 0`, `weather_kind = ""` |
| 6 | [`enter_fort`](#intent-enter-fort) | `false` | [`fort`](#room-fort) | set `current_landmark = "Chimney Rock"` |
| 7 | [`enter_fort`](#intent-enter-fort) | _default_ | `.` | _hint: No fort at this landmark._ |
| 8 | [`face_robbery`](#intent-face-robbery) |  | [`frontier`](#room-frontier) |  |
| 9 | [`give_up_leg`](#intent-give-up-leg) | `world.cycle__leg_c__on_failure < 2` | [`leg_b_executing`](#room-leg-b-executing) | increment `cycle__leg_c__on_failure += 1` · set `breakdown_part = ""`, `current_event_attempts = 0`, `encounter_kind = ""`, `event_kind = ""`, `illness_kind = ""`, `miles_traveled = 0`, `weather_kind = ""` · say "The party turns back toward Fort Kearney." |
| 10 | [`give_up_leg`](#intent-give-up-leg) | _default_ | [`leg_c_error`](#room-leg-c-error) | say "The party has given up too many times — stranded." |
| 11 | [`hunt`](#intent-hunt) |  | [`hunt`](#room-hunt) |  |
| 12 | [`quit`](#intent-quit) |  | [`ended_lost`](#room-ended-lost) | say "The party abandons the journey at Chimney Rock." |
| 13 | [`rest`](#intent-rest) |  | [`rest_room`](#room-rest-room) |  |
| 14 | [`restart_from`](#intent-restart-from) | `slots.stage == 'independence' \|\| slots.stage == 'kansas'` | [`leg_a_executing`](#room-leg-a-executing) | set `breakdown_part = ""`, `current_event_attempts = 0`, `encounter_kind = ""`, `event_kind = ""`, `illness_kind = ""`, `miles_traveled = 0`, `weather_kind = ""` · say "The party doubles back to Independence to retry the run toward Kansas River." |
| 15 | [`restart_from`](#intent-restart-from) | `slots.stage == 'kearney'` | [`leg_b_executing`](#room-leg-b-executing) | set `breakdown_part = ""`, `current_event_attempts = 0`, `encounter_kind = ""`, `event_kind = ""`, `illness_kind = ""`, `miles_traveled = 0`, `weather_kind = ""` · say "The party doubles back to Kansas River to retry the stretch toward Fort Kearney." |
| 16 | [`restart_from`](#intent-restart-from) | `slots.stage == 'chimney'` | [`leg_c_executing`](#room-leg-c-executing) | set `breakdown_part = ""`, `current_event_attempts = 0`, `encounter_kind = ""`, `event_kind = ""`, `illness_kind = ""`, `miles_traveled = 0`, `weather_kind = ""` · say "The party doubles back to Fort Kearney to retry the stretch toward Chimney Rock." |
| 17 | [`restart_from`](#intent-restart-from) | `slots.stage == 'laramie'` | [`leg_d_executing`](#room-leg-d-executing) | set `breakdown_part = ""`, `current_event_attempts = 0`, `encounter_kind = ""`, `event_kind = ""`, `illness_kind = ""`, `miles_traveled = 0`, `weather_kind = ""` · say "The party doubles back to Chimney Rock to retry the stretch toward Fort Laramie." |
| 18 | [`restart_from`](#intent-restart-from) | `slots.stage == 'south_pass'` | [`leg_e_executing`](#room-leg-e-executing) | set `breakdown_part = ""`, `current_event_attempts = 0`, `encounter_kind = ""`, `event_kind = ""`, `illness_kind = ""`, `miles_traveled = 0`, `weather_kind = ""` · say "The party doubles back to Fort Laramie to retry the stretch toward South Pass." |
| 19 | [`restart_from`](#intent-restart-from) | `slots.stage == 'snake'` | [`leg_f_executing`](#room-leg-f-executing) | set `breakdown_part = ""`, `current_event_attempts = 0`, `encounter_kind = ""`, `event_kind = ""`, `illness_kind = ""`, `miles_traveled = 0`, `weather_kind = ""` · say "The party doubles back to South Pass to retry the stretch toward Snake River." |
| 20 | [`restart_from`](#intent-restart-from) | _default_ | `.` | _hint: Unknown restart stage._ |
| 21 | [`scout`](#intent-scout) |  | [`frontier`](#room-frontier) |  |

**Timeout**: after `10d` → `leg_d_executing`

### <a id="room-leg-c-error"></a> `leg_c_error`

Stranded between Fort Kearney and Chimney Rock.

**Shows world**: `day`, `party_alive`, `current_landmark`

**Transitions**:

| # | Intent | Guard | → | Effects |
|---|---|---|---|---|
| 1 | [`quit`](#intent-quit) |  | [`ended_lost`](#room-ended-lost) | say "The party gives up between Fort Kearney and Chimney Rock." |
| 2 | [`retry`](#intent-retry) |  | [`leg_c_executing`](#room-leg-c-executing) | set `current_event_attempts = 0`, `event_kind = ""` |

### <a id="room-leg-c-executing"></a> `leg_c_executing`  _(compound)_

On the trail from Fort Kearney to Chimney Rock (prairie).

**Initial child**: `traveling`

**Shows world**: `day`, `month`, `miles_traveled`, `food_lbs`, `oxen`, `party_alive`, `health_avg`, `pace`, `rations`, `current_landmark`, `event_kind`, `illness_kind`, `illness_severity`, `illness_member`, `breakdown_part`, `weather_kind`, `encounter_kind`, `rng_last`, `rng_counter`

**On enter**:

1. set `current_landmark = "Fort Kearney"`

**Transitions**:

| # | Intent | Guard | → | Effects |
|---|---|---|---|---|
| 1 | [`on_failure`](#intent-on-failure) | `world.cycle__leg_c__on_failure < 2` | [`leg_b_executing`](#room-leg-b-executing) | increment `cycle__leg_c__on_failure += 1` |
| 2 | [`on_failure`](#intent-on-failure) | _default_ | [`leg_c_error`](#room-leg-c-error) | _hint: cycle budget exceeded for on_failure_ |

### <a id="room-leg-c-executing-event-breakdown"></a> `leg_c_executing.event_breakdown`

Wagon breakdown — {{ world.breakdown_part }}.

**Shows world**: `breakdown_part`, `spare_wheels`, `spare_axles`, `spare_tongues`, `day`, `current_event_attempts`, `last_event_prose`

**On enter**:

1. set `breakdown_part = "{{ if world.rng_last % 3 == 0 }}wheel{{ else }}{{ if world.rng_last % 3 == 1 }}axle{{ else }}tongue{{ end }}{{ end }}"`, `current_event_attempts = 0`, `last_event_prose = ""`
2. invoke `host.agent.ask` with `agent = "trail_narrator"`, `args = map[current_landmark:{{ world.current_landmark }} day:{{ world.day }} part:{{ if world.rng_last % 3 == 0 }}wheel{{ else }}{{ if world.rng_last % 3 == 1 }}axle{{ else }}tongue{{ end }}{{ end }} spares_remaining:{{ world.rng_last % 3 == 0 ? world.spare_wheels : (world.rng_last % 3 == 1 ? world.spare_axles : world.spare_tongues) }}]`, `prompt_path = "prompts/event_breakdown.md"`, bind `last_event_prose ← stdout`

**Transitions**:

| # | Intent | Guard | → | Effects |
|---|---|---|---|---|
| 1 | [`look`](#intent-look) |  | `.` |  |
| 2 | [`quit`](#intent-quit) |  | [`ended_lost`](#room-ended-lost) | say "The party abandons the broken wagon." |
| 3 | [`repair`](#intent-repair) | `world.breakdown_part == 'wheel' && world.spare_wheels >= 1` | `../traveling` | invoke `host.transport.post` with `body = "Day {{ world.day }} — fitted the spare wheel and got the wagon rolling again. One less in reserve."`, `phase_id = "leg_c_event_breakdown_wheel_repaired_{{ world.day }}"`, `thread = "{{ run.id }}"`, `title = "Wheel repaired"`, `transport = "tui"` · set `breakdown_part = ""`, `current_event_attempts = 0`, `event_kind = ""`, `spare_wheels = "{{ world.spare_wheels - 1 }}"` · say "Spare wheel installed. Back on the trail." |
| 4 | [`repair`](#intent-repair) | `world.breakdown_part == 'axle' && world.spare_axles >= 1` | `../traveling` | invoke `host.transport.post` with `body = "Day {{ world.day }} — swapped in the spare axle. Took the morning, but the wagon's true again."`, `phase_id = "leg_c_event_breakdown_axle_repaired_{{ world.day }}"`, `thread = "{{ run.id }}"`, `title = "Axle repaired"`, `transport = "tui"` · set `breakdown_part = ""`, `current_event_attempts = 0`, `event_kind = ""`, `spare_axles = "{{ world.spare_axles - 1 }}"` · say "Spare axle installed. Back on the trail." |
| 5 | [`repair`](#intent-repair) | `world.breakdown_part == 'tongue' && world.spare_tongues >= 1` | `../traveling` | invoke `host.transport.post` with `body = "Day {{ world.day }} — pinned in the spare tongue and we hitched the oxen back up."`, `phase_id = "leg_c_event_breakdown_tongue_repaired_{{ world.day }}"`, `thread = "{{ run.id }}"`, `title = "Tongue repaired"`, `transport = "tui"` · set `breakdown_part = ""`, `current_event_attempts = 0`, `event_kind = ""`, `spare_tongues = "{{ world.spare_tongues - 1 }}"` · say "Spare tongue installed. Back on the trail." |
| 6 | [`repair`](#intent-repair) | `world.current_event_attempts < 2` | `.` | _hint: Need a spare {{ world.breakdown_part }} to repair._ · increment `current_event_attempts += 1` · say "No spare {{ world.breakdown_part }} on hand. Try repair again, wait_out, or look." |
| 7 | [`repair`](#intent-repair) | _default_ | `../traveling` | invoke `host.transport.post` with `body = "Day {{ world.day }} — couldn't repair the {{ world.breakdown_part }}. Lashed it together and pressed on at a cost: a member of the party was left behind."`, `phase_id = "leg_c_event_breakdown_failed_{{ world.day }}"`, `thread = "{{ run.id }}"`, `title = "Wagon limping on"`, `transport = "tui"` · set `breakdown_part = ""`, `current_event_attempts = 0`, `event_kind = ""`, `health_avg = "{{ world.health_avg - 20 }}"`, `party_alive = "{{ world.party_alive - 1 }}"` · say "Repeated repair attempts failed; the wagon is patched poorly and the party limps on. A member is left behind." |
| 8 | [`wait_out`](#intent-wait-out) |  | `../traveling` | invoke `host.transport.post` with `body = "Day {{ world.day }} — made camp and improvised a {{ world.breakdown_part }} repair over five days. Slow going, but back on the trail."`, `phase_id = "leg_c_event_breakdown_waited_{{ world.day }}"`, `thread = "{{ run.id }}"`, `title = "Improvised repair"`, `transport = "tui"` · set `breakdown_part = ""`, `current_event_attempts = 0`, `day = "{{ world.day + 5 }}"`, `event_kind = ""` · say "The party makes camp and improvises a repair over five days." |

### <a id="room-leg-c-executing-event-disease"></a> `leg_c_executing.event_disease`

Illness has struck the party ({{ world.illness_kind }}).

**Shows world**: `illness_kind`, `illness_severity`, `illness_treatment`, `illness_member`, `health_avg`, `party_alive`, `food_lbs`, `clothing_sets`, `day`, `current_event_attempts`

**On enter**:

1. set `current_event_attempts = 0`, `health_avg = "{{ world.health_avg - 10 }}"`, `illness_member = "{{ split(world.party_names, ',')[world.rng_last % world.party_alive] }}"`
2. set `illness_kind = "{{ if world.rng_last % 5 == 0 }}dysentery{{ else }}{{ if world.rng_last % 5 == 1 }}cholera{{ else }}{{ if world.rng_last % 5 == 2 }}typhoid{{ else }}{{ if world.rng_last % 5 == 3 }}measles{{ else }}exhaustion{{ end }}{{ end }}{{ end }}{{ end }}"`, `illness_severity = "{{ world.rng_last % 5 + 1 }}"`, `illness_treatment = "rest"`
3. invoke `host.agent.decide` with `agent = "frontier_doctor"`, `args = map[clothing_sets:{{ world.clothing_sets }} current_landmark:{{ world.current_landmark }} food_lbs:{{ world.food_lbs }} health_avg:{{ world.health_avg }} party_alive:{{ world.party_alive }} rng_last:{{ world.rng_last }}]`, `prompt = "prompts/event_disease.md"`, `schema = "mcp/illness.json"`, bind `illness_kind ← submitted.illness`, `illness_severity ← submitted.severity`, `illness_treatment ← submitted.treatment`, on_error → `leg_c_error`

**Transitions**:

| # | Intent | Guard | → | Effects |
|---|---|---|---|---|
| 1 | [`look`](#intent-look) |  | `.` |  |
| 2 | [`move_on`](#intent-move-on) |  | `../traveling` | invoke `host.transport.post` with `body = "Day {{ world.day }} — pushed on through the {{ world.illness_kind }} rather than stop. Health is worse for it, but the wagon rolls."`, `phase_id = "leg_c_event_disease_moved_on_{{ world.day }}"`, `thread = "{{ run.id }}"`, `title = "Pressing on through illness"`, `transport = "tui"` · set `current_event_attempts = 0`, `event_kind = ""`, `health_avg = "{{ world.health_avg - 15 }}"`, `illness_kind = ""`, `illness_member = ""`, `illness_severity = 0`, `illness_treatment = ""` · say "The party presses on. Health worsens." |
| 3 | [`quit`](#intent-quit) |  | [`ended_lost`](#room-ended-lost) | say "The party gives up on the trail, broken by {{ world.illness_kind }}." |
| 4 | [`treat`](#intent-treat) | `world.clothing_sets >= 1 && world.food_lbs >= 50 && world.current_event_attempts < 2` | `../traveling` | invoke `host.transport.post` with `body = "Day {{ world.day }} — the {{ world.illness_kind }} has passed. Rested up, one clothing set used and 50 lbs of food spent on broth and care. Spirits steady, on the move."`, `phase_id = "leg_c_event_disease_treated_{{ world.day }}"`, `thread = "{{ run.id }}"`, `title = "Disease treated"`, `transport = "tui"` · set `clothing_sets = "{{ world.clothing_sets - 1 }}"`, `current_event_attempts = 0`, `event_kind = ""`, `food_lbs = "{{ world.food_lbs - 50 }}"`, `health_avg = "{{ world.health_avg + 20 }}"`, `illness_kind = ""`, `illness_member = ""`, `illness_severity = 0`, `illness_treatment = ""` · say "The party rests and treats the illness." |
| 5 | [`treat`](#intent-treat) | `(world.clothing_sets < 1 \|\| world.food_lbs < 50) && world.current_event_attempts < 2` | `.` | increment `current_event_attempts += 1` · say "Not enough supplies to treat the {{ world.illness_kind }} (need 1 clothing set + 50 lbs food). Try again or wait_out." |
| 6 | [`treat`](#intent-treat) | _default_ | `../traveling` | invoke `host.transport.post` with `body = "Day {{ world.day }} — after repeated attempts, the {{ world.illness_kind }} took one of the party. May the trail remember them."`, `phase_id = "leg_c_event_disease_lost_{{ world.day }}"`, `thread = "{{ run.id }}"`, `title = "Lost to illness"`, `transport = "tui"` · set `current_event_attempts = 0`, `event_kind = ""`, `illness_kind = ""`, `illness_member = ""`, `illness_severity = 0`, `illness_treatment = ""`, `party_alive = "{{ world.party_alive - 1 }}"` · say "After repeated futile attempts, a party member has died of illness." |
| 7 | [`wait_out`](#intent-wait-out) | `world.health_avg < 30` | `../traveling` | invoke `host.transport.post` with `body = "Day {{ world.day }} — the patient was too weak. {{ world.illness_kind }} claimed one of the party while we waited."`, `phase_id = "leg_c_event_disease_wait_lost_{{ world.day }}"`, `thread = "{{ run.id }}"`, `title = "Lost to illness"`, `transport = "tui"` · set `current_event_attempts = 0`, `day = "{{ world.day + 1 }}"`, `event_kind = ""`, `illness_kind = ""`, `illness_member = ""`, `illness_severity = 0`, `illness_treatment = ""`, `party_alive = "{{ world.party_alive - 1 }}"` · say "The patient was too weak. A party member dies of illness." |
| 8 | [`wait_out`](#intent-wait-out) | _default_ | `../traveling` | invoke `host.transport.post` with `body = "Day {{ world.day }} — one day of rest and the {{ world.illness_kind }} let go. We move on tomorrow."`, `phase_id = "leg_c_event_disease_wait_ok_{{ world.day }}"`, `thread = "{{ run.id }}"`, `title = "Illness passed"`, `transport = "tui"` · set `current_event_attempts = 0`, `day = "{{ world.day + 1 }}"`, `event_kind = ""`, `illness_kind = ""`, `illness_member = ""`, `illness_severity = 0`, `illness_treatment = ""` · say "The party rests a day. The illness passes." |

### <a id="room-leg-c-executing-event-encounter"></a> `leg_c_executing.event_encounter`

Encounter on the trail: {{ world.encounter_kind }}.

**Shows world**: `encounter_kind`, `food_lbs`, `clothing_sets`, `last_event_prose`

**On enter**:

1. set `current_event_attempts = 0`, `encounter_kind = "{{ if world.rng_last % 3 == 0 }}trader{{ else }}{{ if world.rng_last % 3 == 1 }}hunter{{ else }}band{{ end }}{{ end }}"`, `last_event_prose = ""`
2. invoke `host.agent.ask` with `agent = "trail_narrator"`, `args = map[clothing_sets:{{ world.clothing_sets }} current_landmark:{{ world.current_landmark }} day:{{ world.day }} food_lbs:{{ world.food_lbs }} kind:{{ if world.rng_last % 3 == 0 }}trader{{ else }}{{ if world.rng_last % 3 == 1 }}hunter{{ else }}band{{ end }}{{ end }}]`, `prompt_path = "prompts/event_encounter.md"`, bind `last_event_prose ← stdout`

**Transitions**:

| # | Intent | Guard | → | Effects |
|---|---|---|---|---|
| 1 | [`accept_trade`](#intent-accept-trade) | `world.food_lbs >= 50` | `../traveling` | invoke `host.transport.post` with `body = "Day {{ world.day }} — traded 50 lbs of food to a {{ world.encounter_kind }} for one clothing set. A fair deal on the trail."`, `phase_id = "leg_c_event_encounter_traded_{{ world.day }}"`, `thread = "{{ run.id }}"`, `title = "Trade with a {{ world.encounter_kind }}"`, `transport = "tui"` · set `clothing_sets = "{{ world.clothing_sets + 1 }}"`, `current_event_attempts = 0`, `encounter_kind = ""`, `event_kind = ""`, `food_lbs = "{{ world.food_lbs - 50 }}"` · say "Trade complete: -50 lbs food, +1 clothing set." |
| 2 | [`accept_trade`](#intent-accept-trade) | _default_ | `.` | _hint: Not enough food to trade._ |
| 3 | [`decline_trade`](#intent-decline-trade) |  | `../traveling` | invoke `host.transport.post` with `body = "Day {{ world.day }} — passed on a trade with a {{ world.encounter_kind }}. The wagon kept rolling."`, `phase_id = "leg_c_event_encounter_declined_{{ world.day }}"`, `thread = "{{ run.id }}"`, `title = "Declined a trade"`, `transport = "tui"` · set `current_event_attempts = 0`, `encounter_kind = ""`, `event_kind = ""` · say "The party declines the trade and presses on." |
| 4 | [`look`](#intent-look) |  | `.` |  |
| 5 | [`move_on`](#intent-move-on) |  | `../traveling` | invoke `host.transport.post` with `body = "Day {{ world.day }} — moved on past the {{ world.encounter_kind }}. No words exchanged."`, `phase_id = "leg_c_event_encounter_moved_on_{{ world.day }}"`, `thread = "{{ run.id }}"`, `title = "Moved on from a {{ world.encounter_kind }}"`, `transport = "tui"` · set `current_event_attempts = 0`, `encounter_kind = ""`, `event_kind = ""` · say "The party moves on past the encounter." |
| 6 | [`quit`](#intent-quit) |  | [`ended_lost`](#room-ended-lost) | say "The party gives up at the encounter." |

### <a id="room-leg-c-executing-event-supply-loss"></a> `leg_c_executing.event_supply_loss`

Supplies lost on the trail.

**Shows world**: `food_lbs`, `oxen`, `rng_last`, `last_event_prose`

**On enter**:

1. set `current_event_attempts = 0`, `last_event = "{{ if world.rng_last % 2 == 0 }}food_loss{{ else }}ox_loss{{ end }}"`, `last_event_prose = ""`
2. set `food_lbs = "{{ world.rng_last % 2 == 0 ? world.food_lbs - (10 + 10 * (world.rng_last % 4)) : world.food_lbs }}"`, `oxen = "{{ world.rng_last % 2 == 0 ? world.oxen : world.oxen - 1 }}"`
3. invoke `host.agent.ask` with `agent = "trail_narrator"`, `args = map[current_landmark:{{ world.current_landmark }} day:{{ world.day }} food_lbs:{{ world.food_lbs }} oxen:{{ world.oxen }} what:{{ world.rng_last % 2 == 0 ? 'food spoiled' : 'ox lame' }}]`, `prompt_path = "prompts/event_supply_loss.md"`, bind `last_event_prose ← stdout`

**Transitions**:

| # | Intent | Guard | → | Effects |
|---|---|---|---|---|
| 1 | [`look`](#intent-look) |  | `.` |  |
| 2 | [`move_on`](#intent-move-on) |  | `../traveling` | invoke `host.transport.post` with `body = "Day {{ world.day }} — {{ world.last_event == 'food_loss' ? 'food spoiled' : 'an ox went lame' }} on the trail. Recovered what we could and pressed on. Food: {{ world.food_lbs }} lbs, oxen: {{ world.oxen }}."`, `phase_id = "leg_c_event_supply_loss_recovered_{{ world.day }}"`, `thread = "{{ run.id }}"`, `title = "Supplies lost"`, `transport = "tui"` · set `current_event_attempts = 0`, `event_kind = ""`, `last_event = ""` · say "The party recovers what it can and presses on." |
| 3 | [`quit`](#intent-quit) |  | [`ended_lost`](#room-ended-lost) | say "The party gives up after losing supplies." |

### <a id="room-leg-c-executing-event-weather"></a> `leg_c_executing.event_weather`

Severe weather: {{ world.weather_kind }}.

**Shows world**: `weather_kind`, `day`, `food_lbs`, `health_avg`, `last_event_prose`

**On enter**:

1. set `current_event_attempts = 0`, `last_event_prose = ""`, `weather_kind = "{{ world.month == 'november' || world.month == 'december' || world.month == 'january' || world.month == 'february' ? 'snow' : (world.month == 'march' || world.month == 'april' || world.month == 'may' ? 'heavy_rain' : (world.month == 'june' || world.month == 'july' || world.month == 'august' ? (world.rng_last % 2 == 0 ? 'hail' : 'fog') : (world.rng_last % 2 == 0 ? 'heavy_rain' : 'fog'))) }}"`
2. invoke `host.agent.ask` with `agent = "trail_narrator"`, `args = map[current_landmark:{{ world.current_landmark }} day:{{ world.day }} kind:{{ world.month == 'november' || world.month == 'december' || world.month == 'january' || world.month == 'february' ? 'snow' : (world.month == 'march' || world.month == 'april' || world.month == 'may' ? 'heavy_rain' : (world.month == 'june' || world.month == 'july' || world.month == 'august' ? (world.rng_last % 2 == 0 ? 'hail' : 'fog') : (world.rng_last % 2 == 0 ? 'heavy_rain' : 'fog'))) }} month:{{ world.month }} terrain:prairie]`, `prompt_path = "prompts/event_weather.md"`, bind `last_event_prose ← stdout`

**Transitions**:

| # | Intent | Guard | → | Effects |
|---|---|---|---|---|
| 1 | [`look`](#intent-look) |  | `.` |  |
| 2 | [`push_on`](#intent-push-on) |  | `../traveling` | invoke `host.transport.post` with `body = "Day {{ world.day }} — pushed on through the {{ world.weather_kind }}. Cold and wet; health worsened by 10."`, `phase_id = "leg_c_event_weather_pushed_on_{{ world.day }}"`, `thread = "{{ run.id }}"`, `title = "Pushed on through the weather"`, `transport = "tui"` · set `current_event_attempts = 0`, `event_kind = ""`, `health_avg = "{{ world.health_avg - 10 }}"`, `weather_kind = ""` · say "The party pushes on through the weather." |
| 3 | [`quit`](#intent-quit) |  | [`ended_lost`](#room-ended-lost) | say "The party gives up in the {{ world.weather_kind }}." |
| 4 | [`wait_out`](#intent-wait-out) |  | `../traveling` | invoke `host.transport.post` with `body = "Day {{ world.day }} — sheltered until the {{ world.weather_kind }} let up. {{ world.weather_kind == 'heavy_rain' ? '20 lbs of food spoiled in the wet.' : 'No supplies lost — only days.' }}"`, `phase_id = "leg_c_event_weather_waited_{{ world.day }}"`, `thread = "{{ run.id }}"`, `title = "Weather passed"`, `transport = "tui"` · set `current_event_attempts = 0`, `day = "{{ world.day + (world.rng_last % 3) + 1 }}"`, `event_kind = ""`, `food_lbs = "{{ world.weather_kind == 'heavy_rain' ? world.food_lbs - 20 : world.food_lbs }}"`, `weather_kind = ""` · say "The party shelters until the weather passes." |

### <a id="room-leg-c-executing-traveling"></a> `leg_c_executing.traveling`

Travelling — Fort Kearney → Chimney Rock.

**Transitions**:

| # | Intent | Guard | → | Effects |
|---|---|---|---|---|
| 1 | [`continue`](#intent-continue) | `world.miles_traveled + int((world.pace == 'grueling' ? 22 : (world.pace == 'strenuous' ? 18 : 14)) * (world.month == 'december' \|\| world.month == 'january' \|\| world.month == 'february' ? 70 : (world.month == 'november' ? 80 : (world.month == 'march' \|\| world.month == 'october' ? 85 : (world.month == 'april' \|\| world.month == 'september' ? 95 : 100)))) / 100) >= 250` | [`leg_c_awaiting_reply`](#room-leg-c-awaiting-reply) | set `day = "{{ world.day + 1 > 30 ? 1 : world.day + 1 }}"`, `food_lbs = "{{ world.food_lbs - (world.rations == 'bare_bones' ? 6 : (world.rations == 'meager' ? 8 : 10)) }}"`, `miles_traveled = "{{ world.miles_traveled + int((world.pace == 'grueling' ? 22 : (world.pace == 'strenuous' ? 18 : 14)) * (world.month == 'december' || world.month == 'january' || world.month == 'february' ? 70 : (world.month == 'november' ? 80 : (world.month == 'march' || world.month == 'october' ? 85 : (world.month == 'april' || world.month == 'september' ? 95 : 100)))) / 100) }}"`, `year = "{{ world.day + 1 > 30 && world.month == 'december' ? world.year + 1 : world.year }}"` · set `month = "{{ world.day + 1 > 30 ? (world.month == 'march' ? 'april' : (world.month == 'april' ? 'may' : (world.month == 'may' ? 'june' : (world.month == 'june' ? 'july' : (world.month == 'july' ? 'august' : (world.month == 'august' ? 'september' : (world.month == 'september' ? 'october' : (world.month == 'october' ? 'november' : (world.month == 'november' ? 'december' : (world.month == 'december' ? 'january' : (world.month == 'january' ? 'february' : (world.month == 'february' ? 'march' : world.month)))))))))))) : world.month }}"` · say "Arrived at Chimney Rock." |
| 2 | [`continue`](#intent-continue) | `world.food_lbs - (world.rations == 'bare_bones' ? 6 : (world.rations == 'meager' ? 8 : 10)) <= 0` | [`ended_lost`](#room-ended-lost) | set `day = "{{ world.day + 1 > 30 ? 1 : world.day + 1 }}"`, `food_lbs = 0`, `year = "{{ world.day + 1 > 30 && world.month == 'december' ? world.year + 1 : world.year }}"` · set `month = "{{ world.day + 1 > 30 ? (world.month == 'march' ? 'april' : (world.month == 'april' ? 'may' : (world.month == 'may' ? 'june' : (world.month == 'june' ? 'july' : (world.month == 'july' ? 'august' : (world.month == 'august' ? 'september' : (world.month == 'september' ? 'october' : (world.month == 'october' ? 'november' : (world.month == 'november' ? 'december' : (world.month == 'december' ? 'january' : (world.month == 'january' ? 'february' : (world.month == 'february' ? 'march' : world.month)))))))))))) : world.month }}"` · say "The party has run out of food between Fort Kearney and Chimney Rock. They starve on the trail." |
| 3 | [`continue`](#intent-continue) | `int(world.miles_traveled + world.rng_counter) % 100 >= 60 && int(world.miles_traveled + world.rng_counter) % 100 < 75` | `../event_disease` | set `rng_last = "{{ int(world.miles_traveled + world.rng_counter) % 100 }}"` · set `day = "{{ world.day + 1 > 30 ? 1 : world.day + 1 }}"`, `event_kind = "disease"`, `food_lbs = "{{ world.food_lbs - (world.rations == 'bare_bones' ? 6 : (world.rations == 'meager' ? 8 : 10)) }}"`, `miles_traveled = "{{ world.miles_traveled + int((world.pace == 'grueling' ? 22 : (world.pace == 'strenuous' ? 18 : 14)) * (world.month == 'december' || world.month == 'january' || world.month == 'february' ? 70 : (world.month == 'november' ? 80 : (world.month == 'march' || world.month == 'october' ? 85 : (world.month == 'april' || world.month == 'september' ? 95 : 100)))) / 100) }}"`, `year = "{{ world.day + 1 > 30 && world.month == 'december' ? world.year + 1 : world.year }}"` · set `month = "{{ world.day + 1 > 30 ? (world.month == 'march' ? 'april' : (world.month == 'april' ? 'may' : (world.month == 'may' ? 'june' : (world.month == 'june' ? 'july' : (world.month == 'july' ? 'august' : (world.month == 'august' ? 'september' : (world.month == 'september' ? 'october' : (world.month == 'october' ? 'november' : (world.month == 'november' ? 'december' : (world.month == 'december' ? 'january' : (world.month == 'january' ? 'february' : (world.month == 'february' ? 'march' : world.month)))))))))))) : world.month }}"` · increment `rng_counter += 1` |
| 4 | [`continue`](#intent-continue) | `int(world.miles_traveled + world.rng_counter) % 100 >= 75 && int(world.miles_traveled + world.rng_counter) % 100 < 85` | `../event_breakdown` | set `rng_last = "{{ int(world.miles_traveled + world.rng_counter) % 100 }}"` · set `day = "{{ world.day + 1 > 30 ? 1 : world.day + 1 }}"`, `event_kind = "breakdown"`, `food_lbs = "{{ world.food_lbs - (world.rations == 'bare_bones' ? 6 : (world.rations == 'meager' ? 8 : 10)) }}"`, `miles_traveled = "{{ world.miles_traveled + int((world.pace == 'grueling' ? 22 : (world.pace == 'strenuous' ? 18 : 14)) * (world.month == 'december' || world.month == 'january' || world.month == 'february' ? 70 : (world.month == 'november' ? 80 : (world.month == 'march' || world.month == 'october' ? 85 : (world.month == 'april' || world.month == 'september' ? 95 : 100)))) / 100) }}"`, `year = "{{ world.day + 1 > 30 && world.month == 'december' ? world.year + 1 : world.year }}"` · set `month = "{{ world.day + 1 > 30 ? (world.month == 'march' ? 'april' : (world.month == 'april' ? 'may' : (world.month == 'may' ? 'june' : (world.month == 'june' ? 'july' : (world.month == 'july' ? 'august' : (world.month == 'august' ? 'september' : (world.month == 'september' ? 'october' : (world.month == 'october' ? 'november' : (world.month == 'november' ? 'december' : (world.month == 'december' ? 'january' : (world.month == 'january' ? 'february' : (world.month == 'february' ? 'march' : world.month)))))))))))) : world.month }}"` · increment `rng_counter += 1` |
| 5 | [`continue`](#intent-continue) | `int(world.miles_traveled + world.rng_counter) % 100 >= 85 && int(world.miles_traveled + world.rng_counter) % 100 < 92` | `../event_weather` | set `rng_last = "{{ int(world.miles_traveled + world.rng_counter) % 100 }}"` · set `day = "{{ world.day + 1 > 30 ? 1 : world.day + 1 }}"`, `event_kind = "weather"`, `food_lbs = "{{ world.food_lbs - (world.rations == 'bare_bones' ? 6 : (world.rations == 'meager' ? 8 : 10)) }}"`, `miles_traveled = "{{ world.miles_traveled + int((world.pace == 'grueling' ? 22 : (world.pace == 'strenuous' ? 18 : 14)) * (world.month == 'december' || world.month == 'january' || world.month == 'february' ? 70 : (world.month == 'november' ? 80 : (world.month == 'march' || world.month == 'october' ? 85 : (world.month == 'april' || world.month == 'september' ? 95 : 100)))) / 100) }}"`, `year = "{{ world.day + 1 > 30 && world.month == 'december' ? world.year + 1 : world.year }}"` · set `month = "{{ world.day + 1 > 30 ? (world.month == 'march' ? 'april' : (world.month == 'april' ? 'may' : (world.month == 'may' ? 'june' : (world.month == 'june' ? 'july' : (world.month == 'july' ? 'august' : (world.month == 'august' ? 'september' : (world.month == 'september' ? 'october' : (world.month == 'october' ? 'november' : (world.month == 'november' ? 'december' : (world.month == 'december' ? 'january' : (world.month == 'january' ? 'february' : (world.month == 'february' ? 'march' : world.month)))))))))))) : world.month }}"` · increment `rng_counter += 1` |
| 6 | [`continue`](#intent-continue) | `int(world.miles_traveled + world.rng_counter) % 100 >= 92 && int(world.miles_traveled + world.rng_counter) % 100 < 97` | `../event_encounter` | set `rng_last = "{{ int(world.miles_traveled + world.rng_counter) % 100 }}"` · set `day = "{{ world.day + 1 > 30 ? 1 : world.day + 1 }}"`, `event_kind = "encounter"`, `food_lbs = "{{ world.food_lbs - (world.rations == 'bare_bones' ? 6 : (world.rations == 'meager' ? 8 : 10)) }}"`, `miles_traveled = "{{ world.miles_traveled + int((world.pace == 'grueling' ? 22 : (world.pace == 'strenuous' ? 18 : 14)) * (world.month == 'december' || world.month == 'january' || world.month == 'february' ? 70 : (world.month == 'november' ? 80 : (world.month == 'march' || world.month == 'october' ? 85 : (world.month == 'april' || world.month == 'september' ? 95 : 100)))) / 100) }}"`, `year = "{{ world.day + 1 > 30 && world.month == 'december' ? world.year + 1 : world.year }}"` · set `month = "{{ world.day + 1 > 30 ? (world.month == 'march' ? 'april' : (world.month == 'april' ? 'may' : (world.month == 'may' ? 'june' : (world.month == 'june' ? 'july' : (world.month == 'july' ? 'august' : (world.month == 'august' ? 'september' : (world.month == 'september' ? 'october' : (world.month == 'october' ? 'november' : (world.month == 'november' ? 'december' : (world.month == 'december' ? 'january' : (world.month == 'january' ? 'february' : (world.month == 'february' ? 'march' : world.month)))))))))))) : world.month }}"` · increment `rng_counter += 1` |
| 7 | [`continue`](#intent-continue) | `int(world.miles_traveled + world.rng_counter) % 100 >= 97` | `../event_supply_loss` | set `rng_last = "{{ int(world.miles_traveled + world.rng_counter) % 100 }}"` · set `day = "{{ world.day + 1 > 30 ? 1 : world.day + 1 }}"`, `event_kind = "supply_loss"`, `food_lbs = "{{ world.food_lbs - (world.rations == 'bare_bones' ? 6 : (world.rations == 'meager' ? 8 : 10)) }}"`, `miles_traveled = "{{ world.miles_traveled + int((world.pace == 'grueling' ? 22 : (world.pace == 'strenuous' ? 18 : 14)) * (world.month == 'december' || world.month == 'january' || world.month == 'february' ? 70 : (world.month == 'november' ? 80 : (world.month == 'march' || world.month == 'october' ? 85 : (world.month == 'april' || world.month == 'september' ? 95 : 100)))) / 100) }}"`, `year = "{{ world.day + 1 > 30 && world.month == 'december' ? world.year + 1 : world.year }}"` · set `month = "{{ world.day + 1 > 30 ? (world.month == 'march' ? 'april' : (world.month == 'april' ? 'may' : (world.month == 'may' ? 'june' : (world.month == 'june' ? 'july' : (world.month == 'july' ? 'august' : (world.month == 'august' ? 'september' : (world.month == 'september' ? 'october' : (world.month == 'october' ? 'november' : (world.month == 'november' ? 'december' : (world.month == 'december' ? 'january' : (world.month == 'january' ? 'february' : (world.month == 'february' ? 'march' : world.month)))))))))))) : world.month }}"` · increment `rng_counter += 1` |
| 8 | [`continue`](#intent-continue) | _default_ | `.` | set `rng_last = "{{ int(world.miles_traveled + world.rng_counter) % 100 }}"` · set `day = "{{ world.day + 1 > 30 ? 1 : world.day + 1 }}"`, `event_kind = ""`, `food_lbs = "{{ world.food_lbs - (world.rations == 'bare_bones' ? 6 : (world.rations == 'meager' ? 8 : 10)) }}"`, `miles_traveled = "{{ world.miles_traveled + int((world.pace == 'grueling' ? 22 : (world.pace == 'strenuous' ? 18 : 14)) * (world.month == 'december' || world.month == 'january' || world.month == 'february' ? 70 : (world.month == 'november' ? 80 : (world.month == 'march' || world.month == 'october' ? 85 : (world.month == 'april' || world.month == 'september' ? 95 : 100)))) / 100) }}"`, `year = "{{ world.day + 1 > 30 && world.month == 'december' ? world.year + 1 : world.year }}"` · set `month = "{{ world.day + 1 > 30 ? (world.month == 'march' ? 'april' : (world.month == 'april' ? 'may' : (world.month == 'may' ? 'june' : (world.month == 'june' ? 'july' : (world.month == 'july' ? 'august' : (world.month == 'august' ? 'september' : (world.month == 'september' ? 'october' : (world.month == 'october' ? 'november' : (world.month == 'november' ? 'december' : (world.month == 'december' ? 'january' : (world.month == 'january' ? 'february' : (world.month == 'february' ? 'march' : world.month)))))))))))) : world.month }}"` · increment `rng_counter += 1` |
| 9 | [`look`](#intent-look) |  | `.` |  |
| 10 | [`quit`](#intent-quit) |  | [`ended_lost`](#room-ended-lost) | say "The party abandons the journey between Fort Kearney and Chimney Rock." |
| 11 | [`set_pace`](#intent-set-pace) |  | `.` | set `pace = "{{ slots.pace }}"` · say "Pace set to {{ slots.pace }}." |
| 12 | [`set_rations`](#intent-set-rations) |  | `.` | set `rations = "{{ slots.rations }}"` · say "Rations set to {{ slots.rations }}." |

### <a id="room-leg-d-awaiting-reply"></a> `leg_d_awaiting_reply`

Arrived at Fort Laramie (prairie).

**Shows world**: `day`, `miles_traveled`, `food_lbs`, `oxen`, `party_alive`, `health_avg`, `illness_kind`, `illness_severity`, `illness_member`, `last_landmark_prose`

**On enter**:

1. set `last_landmark_prose = ""`
2. invoke `host.transport.post` with `body = "Day {{ world.day }}, {{ world.month }} {{ world.year }}. We rolled into **Fort Laramie** (prairie) at last.\n\n- Food: {{ world.food_lbs }} lbs\n- Oxen: {{ world.oxen }}\n- Party: {{ world.party_alive }} alive\n- Health: {{ world.health_avg }}\n"`, `phase_id = "leg_d_arrival"`, `thread = "{{ run.id }}"`, `title = "Day {{ world.day }}: Fort Laramie"`, `transport = "tui"`
3. invoke `host.agent.ask` with `agent = "trail_narrator"`, `args = map[day:{{ world.day }} food_lbs:{{ world.food_lbs }} landmark:Fort Laramie miles_traveled:{{ world.miles_traveled }} month:{{ world.month }} party_alive:{{ world.party_alive }} year:{{ world.year }}]`, `prompt_path = "prompts/landmark_arrival.md"`, bind `last_landmark_prose ← stdout`

**Transitions**:

| # | Intent | Guard | → | Effects |
|---|---|---|---|---|
| 1 | [`approach_river`](#intent-approach-river) | `false` | [`river_crossing`](#room-river-crossing) | set `current_landmark = "Fort Laramie"`, `river_depth_ft = "{{ int(0 * (world.month == 'april' ? 160 : (world.month == 'march' ? 140 : (world.month == 'may' ? 130 : (world.month == 'june' ? 100 : (world.month == 'july' ? 80 : (world.month == 'august' ? 70 : (world.month == 'september' ? 80 : 100))))))) / 100) }}"`, `river_width_ft = "{{ int(0) }}"` |
| 2 | [`approach_river`](#intent-approach-river) | _default_ | `.` | _hint: No river at this landmark._ |
| 3 | [`consult_guide`](#intent-consult-guide) |  | [`trail_guide`](#room-trail-guide) | set `last_job_originating_state = "leg_d_awaiting_reply"` · _(no-history)_ |
| 4 | [`continue`](#intent-continue) | `'Fort Laramie' == 'South Pass' && (world.month == 'october' \|\| world.month == 'november' \|\| world.month == 'december' \|\| world.month == 'january' \|\| world.month == 'february')` | [`snow_blocked`](#room-snow-blocked) | set `current_landmark = "Fort Laramie"` · say "South Pass is snowed in. The wagons cannot get through." |
| 5 | [`continue`](#intent-continue) | _default_ | [`leg_e_executing`](#room-leg-e-executing) | set `breakdown_part = ""`, `current_event_attempts = 0`, `current_landmark = "Fort Laramie"`, `encounter_kind = ""`, `event_kind = ""`, `illness_kind = ""`, `miles_traveled = 0`, `weather_kind = ""` |
| 6 | [`enter_fort`](#intent-enter-fort) | `true` | [`fort`](#room-fort) | set `current_landmark = "Fort Laramie"` |
| 7 | [`enter_fort`](#intent-enter-fort) | _default_ | `.` | _hint: No fort at this landmark._ |
| 8 | [`face_robbery`](#intent-face-robbery) |  | [`frontier`](#room-frontier) |  |
| 9 | [`give_up_leg`](#intent-give-up-leg) | `world.cycle__leg_d__on_failure < 2` | [`leg_c_executing`](#room-leg-c-executing) | increment `cycle__leg_d__on_failure += 1` · set `breakdown_part = ""`, `current_event_attempts = 0`, `encounter_kind = ""`, `event_kind = ""`, `illness_kind = ""`, `miles_traveled = 0`, `weather_kind = ""` · say "The party turns back toward Chimney Rock." |
| 10 | [`give_up_leg`](#intent-give-up-leg) | _default_ | [`leg_d_error`](#room-leg-d-error) | say "The party has given up too many times — stranded." |
| 11 | [`hunt`](#intent-hunt) |  | [`hunt`](#room-hunt) |  |
| 12 | [`quit`](#intent-quit) |  | [`ended_lost`](#room-ended-lost) | say "The party abandons the journey at Fort Laramie." |
| 13 | [`rest`](#intent-rest) |  | [`rest_room`](#room-rest-room) |  |
| 14 | [`restart_from`](#intent-restart-from) | `slots.stage == 'independence' \|\| slots.stage == 'kansas'` | [`leg_a_executing`](#room-leg-a-executing) | set `breakdown_part = ""`, `current_event_attempts = 0`, `encounter_kind = ""`, `event_kind = ""`, `illness_kind = ""`, `miles_traveled = 0`, `weather_kind = ""` · say "The party doubles back to Independence to retry the run toward Kansas River." |
| 15 | [`restart_from`](#intent-restart-from) | `slots.stage == 'kearney'` | [`leg_b_executing`](#room-leg-b-executing) | set `breakdown_part = ""`, `current_event_attempts = 0`, `encounter_kind = ""`, `event_kind = ""`, `illness_kind = ""`, `miles_traveled = 0`, `weather_kind = ""` · say "The party doubles back to Kansas River to retry the stretch toward Fort Kearney." |
| 16 | [`restart_from`](#intent-restart-from) | `slots.stage == 'chimney'` | [`leg_c_executing`](#room-leg-c-executing) | set `breakdown_part = ""`, `current_event_attempts = 0`, `encounter_kind = ""`, `event_kind = ""`, `illness_kind = ""`, `miles_traveled = 0`, `weather_kind = ""` · say "The party doubles back to Fort Kearney to retry the stretch toward Chimney Rock." |
| 17 | [`restart_from`](#intent-restart-from) | `slots.stage == 'laramie'` | [`leg_d_executing`](#room-leg-d-executing) | set `breakdown_part = ""`, `current_event_attempts = 0`, `encounter_kind = ""`, `event_kind = ""`, `illness_kind = ""`, `miles_traveled = 0`, `weather_kind = ""` · say "The party doubles back to Chimney Rock to retry the stretch toward Fort Laramie." |
| 18 | [`restart_from`](#intent-restart-from) | `slots.stage == 'south_pass'` | [`leg_e_executing`](#room-leg-e-executing) | set `breakdown_part = ""`, `current_event_attempts = 0`, `encounter_kind = ""`, `event_kind = ""`, `illness_kind = ""`, `miles_traveled = 0`, `weather_kind = ""` · say "The party doubles back to Fort Laramie to retry the stretch toward South Pass." |
| 19 | [`restart_from`](#intent-restart-from) | `slots.stage == 'snake'` | [`leg_f_executing`](#room-leg-f-executing) | set `breakdown_part = ""`, `current_event_attempts = 0`, `encounter_kind = ""`, `event_kind = ""`, `illness_kind = ""`, `miles_traveled = 0`, `weather_kind = ""` · say "The party doubles back to South Pass to retry the stretch toward Snake River." |
| 20 | [`restart_from`](#intent-restart-from) | _default_ | `.` | _hint: Unknown restart stage._ |
| 21 | [`scout`](#intent-scout) |  | [`frontier`](#room-frontier) |  |

**Timeout**: after `10d` → `leg_e_executing`

### <a id="room-leg-d-error"></a> `leg_d_error`

Stranded between Chimney Rock and Fort Laramie.

**Shows world**: `day`, `party_alive`, `current_landmark`

**Transitions**:

| # | Intent | Guard | → | Effects |
|---|---|---|---|---|
| 1 | [`quit`](#intent-quit) |  | [`ended_lost`](#room-ended-lost) | say "The party gives up between Chimney Rock and Fort Laramie." |
| 2 | [`retry`](#intent-retry) |  | [`leg_d_executing`](#room-leg-d-executing) | set `current_event_attempts = 0`, `event_kind = ""` |

### <a id="room-leg-d-executing"></a> `leg_d_executing`  _(compound)_

On the trail from Chimney Rock to Fort Laramie (prairie).

**Initial child**: `traveling`

**Shows world**: `day`, `month`, `miles_traveled`, `food_lbs`, `oxen`, `party_alive`, `health_avg`, `pace`, `rations`, `current_landmark`, `event_kind`, `illness_kind`, `illness_severity`, `illness_member`, `breakdown_part`, `weather_kind`, `encounter_kind`, `rng_last`, `rng_counter`

**On enter**:

1. set `current_landmark = "Chimney Rock"`

**Transitions**:

| # | Intent | Guard | → | Effects |
|---|---|---|---|---|
| 1 | [`on_failure`](#intent-on-failure) | `world.cycle__leg_d__on_failure < 2` | [`leg_c_executing`](#room-leg-c-executing) | increment `cycle__leg_d__on_failure += 1` |
| 2 | [`on_failure`](#intent-on-failure) | _default_ | [`leg_d_error`](#room-leg-d-error) | _hint: cycle budget exceeded for on_failure_ |

### <a id="room-leg-d-executing-event-breakdown"></a> `leg_d_executing.event_breakdown`

Wagon breakdown — {{ world.breakdown_part }}.

**Shows world**: `breakdown_part`, `spare_wheels`, `spare_axles`, `spare_tongues`, `day`, `current_event_attempts`, `last_event_prose`

**On enter**:

1. set `breakdown_part = "{{ if world.rng_last % 3 == 0 }}wheel{{ else }}{{ if world.rng_last % 3 == 1 }}axle{{ else }}tongue{{ end }}{{ end }}"`, `current_event_attempts = 0`, `last_event_prose = ""`
2. invoke `host.agent.ask` with `agent = "trail_narrator"`, `args = map[current_landmark:{{ world.current_landmark }} day:{{ world.day }} part:{{ if world.rng_last % 3 == 0 }}wheel{{ else }}{{ if world.rng_last % 3 == 1 }}axle{{ else }}tongue{{ end }}{{ end }} spares_remaining:{{ world.rng_last % 3 == 0 ? world.spare_wheels : (world.rng_last % 3 == 1 ? world.spare_axles : world.spare_tongues) }}]`, `prompt_path = "prompts/event_breakdown.md"`, bind `last_event_prose ← stdout`

**Transitions**:

| # | Intent | Guard | → | Effects |
|---|---|---|---|---|
| 1 | [`look`](#intent-look) |  | `.` |  |
| 2 | [`quit`](#intent-quit) |  | [`ended_lost`](#room-ended-lost) | say "The party abandons the broken wagon." |
| 3 | [`repair`](#intent-repair) | `world.breakdown_part == 'wheel' && world.spare_wheels >= 1` | `../traveling` | invoke `host.transport.post` with `body = "Day {{ world.day }} — fitted the spare wheel and got the wagon rolling again. One less in reserve."`, `phase_id = "leg_d_event_breakdown_wheel_repaired_{{ world.day }}"`, `thread = "{{ run.id }}"`, `title = "Wheel repaired"`, `transport = "tui"` · set `breakdown_part = ""`, `current_event_attempts = 0`, `event_kind = ""`, `spare_wheels = "{{ world.spare_wheels - 1 }}"` · say "Spare wheel installed. Back on the trail." |
| 4 | [`repair`](#intent-repair) | `world.breakdown_part == 'axle' && world.spare_axles >= 1` | `../traveling` | invoke `host.transport.post` with `body = "Day {{ world.day }} — swapped in the spare axle. Took the morning, but the wagon's true again."`, `phase_id = "leg_d_event_breakdown_axle_repaired_{{ world.day }}"`, `thread = "{{ run.id }}"`, `title = "Axle repaired"`, `transport = "tui"` · set `breakdown_part = ""`, `current_event_attempts = 0`, `event_kind = ""`, `spare_axles = "{{ world.spare_axles - 1 }}"` · say "Spare axle installed. Back on the trail." |
| 5 | [`repair`](#intent-repair) | `world.breakdown_part == 'tongue' && world.spare_tongues >= 1` | `../traveling` | invoke `host.transport.post` with `body = "Day {{ world.day }} — pinned in the spare tongue and we hitched the oxen back up."`, `phase_id = "leg_d_event_breakdown_tongue_repaired_{{ world.day }}"`, `thread = "{{ run.id }}"`, `title = "Tongue repaired"`, `transport = "tui"` · set `breakdown_part = ""`, `current_event_attempts = 0`, `event_kind = ""`, `spare_tongues = "{{ world.spare_tongues - 1 }}"` · say "Spare tongue installed. Back on the trail." |
| 6 | [`repair`](#intent-repair) | `world.current_event_attempts < 2` | `.` | _hint: Need a spare {{ world.breakdown_part }} to repair._ · increment `current_event_attempts += 1` · say "No spare {{ world.breakdown_part }} on hand. Try repair again, wait_out, or look." |
| 7 | [`repair`](#intent-repair) | _default_ | `../traveling` | invoke `host.transport.post` with `body = "Day {{ world.day }} — couldn't repair the {{ world.breakdown_part }}. Lashed it together and pressed on at a cost: a member of the party was left behind."`, `phase_id = "leg_d_event_breakdown_failed_{{ world.day }}"`, `thread = "{{ run.id }}"`, `title = "Wagon limping on"`, `transport = "tui"` · set `breakdown_part = ""`, `current_event_attempts = 0`, `event_kind = ""`, `health_avg = "{{ world.health_avg - 20 }}"`, `party_alive = "{{ world.party_alive - 1 }}"` · say "Repeated repair attempts failed; the wagon is patched poorly and the party limps on. A member is left behind." |
| 8 | [`wait_out`](#intent-wait-out) |  | `../traveling` | invoke `host.transport.post` with `body = "Day {{ world.day }} — made camp and improvised a {{ world.breakdown_part }} repair over five days. Slow going, but back on the trail."`, `phase_id = "leg_d_event_breakdown_waited_{{ world.day }}"`, `thread = "{{ run.id }}"`, `title = "Improvised repair"`, `transport = "tui"` · set `breakdown_part = ""`, `current_event_attempts = 0`, `day = "{{ world.day + 5 }}"`, `event_kind = ""` · say "The party makes camp and improvises a repair over five days." |

### <a id="room-leg-d-executing-event-disease"></a> `leg_d_executing.event_disease`

Illness has struck the party ({{ world.illness_kind }}).

**Shows world**: `illness_kind`, `illness_severity`, `illness_treatment`, `illness_member`, `health_avg`, `party_alive`, `food_lbs`, `clothing_sets`, `day`, `current_event_attempts`

**On enter**:

1. set `current_event_attempts = 0`, `health_avg = "{{ world.health_avg - 10 }}"`, `illness_member = "{{ split(world.party_names, ',')[world.rng_last % world.party_alive] }}"`
2. set `illness_kind = "{{ if world.rng_last % 5 == 0 }}dysentery{{ else }}{{ if world.rng_last % 5 == 1 }}cholera{{ else }}{{ if world.rng_last % 5 == 2 }}typhoid{{ else }}{{ if world.rng_last % 5 == 3 }}measles{{ else }}exhaustion{{ end }}{{ end }}{{ end }}{{ end }}"`, `illness_severity = "{{ world.rng_last % 5 + 1 }}"`, `illness_treatment = "rest"`
3. invoke `host.agent.decide` with `agent = "frontier_doctor"`, `args = map[clothing_sets:{{ world.clothing_sets }} current_landmark:{{ world.current_landmark }} food_lbs:{{ world.food_lbs }} health_avg:{{ world.health_avg }} party_alive:{{ world.party_alive }} rng_last:{{ world.rng_last }}]`, `prompt = "prompts/event_disease.md"`, `schema = "mcp/illness.json"`, bind `illness_kind ← submitted.illness`, `illness_severity ← submitted.severity`, `illness_treatment ← submitted.treatment`, on_error → `leg_d_error`

**Transitions**:

| # | Intent | Guard | → | Effects |
|---|---|---|---|---|
| 1 | [`look`](#intent-look) |  | `.` |  |
| 2 | [`move_on`](#intent-move-on) |  | `../traveling` | invoke `host.transport.post` with `body = "Day {{ world.day }} — pushed on through the {{ world.illness_kind }} rather than stop. Health is worse for it, but the wagon rolls."`, `phase_id = "leg_d_event_disease_moved_on_{{ world.day }}"`, `thread = "{{ run.id }}"`, `title = "Pressing on through illness"`, `transport = "tui"` · set `current_event_attempts = 0`, `event_kind = ""`, `health_avg = "{{ world.health_avg - 15 }}"`, `illness_kind = ""`, `illness_member = ""`, `illness_severity = 0`, `illness_treatment = ""` · say "The party presses on. Health worsens." |
| 3 | [`quit`](#intent-quit) |  | [`ended_lost`](#room-ended-lost) | say "The party gives up on the trail, broken by {{ world.illness_kind }}." |
| 4 | [`treat`](#intent-treat) | `world.clothing_sets >= 1 && world.food_lbs >= 50 && world.current_event_attempts < 2` | `../traveling` | invoke `host.transport.post` with `body = "Day {{ world.day }} — the {{ world.illness_kind }} has passed. Rested up, one clothing set used and 50 lbs of food spent on broth and care. Spirits steady, on the move."`, `phase_id = "leg_d_event_disease_treated_{{ world.day }}"`, `thread = "{{ run.id }}"`, `title = "Disease treated"`, `transport = "tui"` · set `clothing_sets = "{{ world.clothing_sets - 1 }}"`, `current_event_attempts = 0`, `event_kind = ""`, `food_lbs = "{{ world.food_lbs - 50 }}"`, `health_avg = "{{ world.health_avg + 20 }}"`, `illness_kind = ""`, `illness_member = ""`, `illness_severity = 0`, `illness_treatment = ""` · say "The party rests and treats the illness." |
| 5 | [`treat`](#intent-treat) | `(world.clothing_sets < 1 \|\| world.food_lbs < 50) && world.current_event_attempts < 2` | `.` | increment `current_event_attempts += 1` · say "Not enough supplies to treat the {{ world.illness_kind }} (need 1 clothing set + 50 lbs food). Try again or wait_out." |
| 6 | [`treat`](#intent-treat) | _default_ | `../traveling` | invoke `host.transport.post` with `body = "Day {{ world.day }} — after repeated attempts, the {{ world.illness_kind }} took one of the party. May the trail remember them."`, `phase_id = "leg_d_event_disease_lost_{{ world.day }}"`, `thread = "{{ run.id }}"`, `title = "Lost to illness"`, `transport = "tui"` · set `current_event_attempts = 0`, `event_kind = ""`, `illness_kind = ""`, `illness_member = ""`, `illness_severity = 0`, `illness_treatment = ""`, `party_alive = "{{ world.party_alive - 1 }}"` · say "After repeated futile attempts, a party member has died of illness." |
| 7 | [`wait_out`](#intent-wait-out) | `world.health_avg < 30` | `../traveling` | invoke `host.transport.post` with `body = "Day {{ world.day }} — the patient was too weak. {{ world.illness_kind }} claimed one of the party while we waited."`, `phase_id = "leg_d_event_disease_wait_lost_{{ world.day }}"`, `thread = "{{ run.id }}"`, `title = "Lost to illness"`, `transport = "tui"` · set `current_event_attempts = 0`, `day = "{{ world.day + 1 }}"`, `event_kind = ""`, `illness_kind = ""`, `illness_member = ""`, `illness_severity = 0`, `illness_treatment = ""`, `party_alive = "{{ world.party_alive - 1 }}"` · say "The patient was too weak. A party member dies of illness." |
| 8 | [`wait_out`](#intent-wait-out) | _default_ | `../traveling` | invoke `host.transport.post` with `body = "Day {{ world.day }} — one day of rest and the {{ world.illness_kind }} let go. We move on tomorrow."`, `phase_id = "leg_d_event_disease_wait_ok_{{ world.day }}"`, `thread = "{{ run.id }}"`, `title = "Illness passed"`, `transport = "tui"` · set `current_event_attempts = 0`, `day = "{{ world.day + 1 }}"`, `event_kind = ""`, `illness_kind = ""`, `illness_member = ""`, `illness_severity = 0`, `illness_treatment = ""` · say "The party rests a day. The illness passes." |

### <a id="room-leg-d-executing-event-encounter"></a> `leg_d_executing.event_encounter`

Encounter on the trail: {{ world.encounter_kind }}.

**Shows world**: `encounter_kind`, `food_lbs`, `clothing_sets`, `last_event_prose`

**On enter**:

1. set `current_event_attempts = 0`, `encounter_kind = "{{ if world.rng_last % 3 == 0 }}trader{{ else }}{{ if world.rng_last % 3 == 1 }}hunter{{ else }}band{{ end }}{{ end }}"`, `last_event_prose = ""`
2. invoke `host.agent.ask` with `agent = "trail_narrator"`, `args = map[clothing_sets:{{ world.clothing_sets }} current_landmark:{{ world.current_landmark }} day:{{ world.day }} food_lbs:{{ world.food_lbs }} kind:{{ if world.rng_last % 3 == 0 }}trader{{ else }}{{ if world.rng_last % 3 == 1 }}hunter{{ else }}band{{ end }}{{ end }}]`, `prompt_path = "prompts/event_encounter.md"`, bind `last_event_prose ← stdout`

**Transitions**:

| # | Intent | Guard | → | Effects |
|---|---|---|---|---|
| 1 | [`accept_trade`](#intent-accept-trade) | `world.food_lbs >= 50` | `../traveling` | invoke `host.transport.post` with `body = "Day {{ world.day }} — traded 50 lbs of food to a {{ world.encounter_kind }} for one clothing set. A fair deal on the trail."`, `phase_id = "leg_d_event_encounter_traded_{{ world.day }}"`, `thread = "{{ run.id }}"`, `title = "Trade with a {{ world.encounter_kind }}"`, `transport = "tui"` · set `clothing_sets = "{{ world.clothing_sets + 1 }}"`, `current_event_attempts = 0`, `encounter_kind = ""`, `event_kind = ""`, `food_lbs = "{{ world.food_lbs - 50 }}"` · say "Trade complete: -50 lbs food, +1 clothing set." |
| 2 | [`accept_trade`](#intent-accept-trade) | _default_ | `.` | _hint: Not enough food to trade._ |
| 3 | [`decline_trade`](#intent-decline-trade) |  | `../traveling` | invoke `host.transport.post` with `body = "Day {{ world.day }} — passed on a trade with a {{ world.encounter_kind }}. The wagon kept rolling."`, `phase_id = "leg_d_event_encounter_declined_{{ world.day }}"`, `thread = "{{ run.id }}"`, `title = "Declined a trade"`, `transport = "tui"` · set `current_event_attempts = 0`, `encounter_kind = ""`, `event_kind = ""` · say "The party declines the trade and presses on." |
| 4 | [`look`](#intent-look) |  | `.` |  |
| 5 | [`move_on`](#intent-move-on) |  | `../traveling` | invoke `host.transport.post` with `body = "Day {{ world.day }} — moved on past the {{ world.encounter_kind }}. No words exchanged."`, `phase_id = "leg_d_event_encounter_moved_on_{{ world.day }}"`, `thread = "{{ run.id }}"`, `title = "Moved on from a {{ world.encounter_kind }}"`, `transport = "tui"` · set `current_event_attempts = 0`, `encounter_kind = ""`, `event_kind = ""` · say "The party moves on past the encounter." |
| 6 | [`quit`](#intent-quit) |  | [`ended_lost`](#room-ended-lost) | say "The party gives up at the encounter." |

### <a id="room-leg-d-executing-event-supply-loss"></a> `leg_d_executing.event_supply_loss`

Supplies lost on the trail.

**Shows world**: `food_lbs`, `oxen`, `rng_last`, `last_event_prose`

**On enter**:

1. set `current_event_attempts = 0`, `last_event = "{{ if world.rng_last % 2 == 0 }}food_loss{{ else }}ox_loss{{ end }}"`, `last_event_prose = ""`
2. set `food_lbs = "{{ world.rng_last % 2 == 0 ? world.food_lbs - (10 + 10 * (world.rng_last % 4)) : world.food_lbs }}"`, `oxen = "{{ world.rng_last % 2 == 0 ? world.oxen : world.oxen - 1 }}"`
3. invoke `host.agent.ask` with `agent = "trail_narrator"`, `args = map[current_landmark:{{ world.current_landmark }} day:{{ world.day }} food_lbs:{{ world.food_lbs }} oxen:{{ world.oxen }} what:{{ world.rng_last % 2 == 0 ? 'food spoiled' : 'ox lame' }}]`, `prompt_path = "prompts/event_supply_loss.md"`, bind `last_event_prose ← stdout`

**Transitions**:

| # | Intent | Guard | → | Effects |
|---|---|---|---|---|
| 1 | [`look`](#intent-look) |  | `.` |  |
| 2 | [`move_on`](#intent-move-on) |  | `../traveling` | invoke `host.transport.post` with `body = "Day {{ world.day }} — {{ world.last_event == 'food_loss' ? 'food spoiled' : 'an ox went lame' }} on the trail. Recovered what we could and pressed on. Food: {{ world.food_lbs }} lbs, oxen: {{ world.oxen }}."`, `phase_id = "leg_d_event_supply_loss_recovered_{{ world.day }}"`, `thread = "{{ run.id }}"`, `title = "Supplies lost"`, `transport = "tui"` · set `current_event_attempts = 0`, `event_kind = ""`, `last_event = ""` · say "The party recovers what it can and presses on." |
| 3 | [`quit`](#intent-quit) |  | [`ended_lost`](#room-ended-lost) | say "The party gives up after losing supplies." |

### <a id="room-leg-d-executing-event-weather"></a> `leg_d_executing.event_weather`

Severe weather: {{ world.weather_kind }}.

**Shows world**: `weather_kind`, `day`, `food_lbs`, `health_avg`, `last_event_prose`

**On enter**:

1. set `current_event_attempts = 0`, `last_event_prose = ""`, `weather_kind = "{{ world.month == 'november' || world.month == 'december' || world.month == 'january' || world.month == 'february' ? 'snow' : (world.month == 'march' || world.month == 'april' || world.month == 'may' ? 'heavy_rain' : (world.month == 'june' || world.month == 'july' || world.month == 'august' ? (world.rng_last % 2 == 0 ? 'hail' : 'fog') : (world.rng_last % 2 == 0 ? 'heavy_rain' : 'fog'))) }}"`
2. invoke `host.agent.ask` with `agent = "trail_narrator"`, `args = map[current_landmark:{{ world.current_landmark }} day:{{ world.day }} kind:{{ world.month == 'november' || world.month == 'december' || world.month == 'january' || world.month == 'february' ? 'snow' : (world.month == 'march' || world.month == 'april' || world.month == 'may' ? 'heavy_rain' : (world.month == 'june' || world.month == 'july' || world.month == 'august' ? (world.rng_last % 2 == 0 ? 'hail' : 'fog') : (world.rng_last % 2 == 0 ? 'heavy_rain' : 'fog'))) }} month:{{ world.month }} terrain:prairie]`, `prompt_path = "prompts/event_weather.md"`, bind `last_event_prose ← stdout`

**Transitions**:

| # | Intent | Guard | → | Effects |
|---|---|---|---|---|
| 1 | [`look`](#intent-look) |  | `.` |  |
| 2 | [`push_on`](#intent-push-on) |  | `../traveling` | invoke `host.transport.post` with `body = "Day {{ world.day }} — pushed on through the {{ world.weather_kind }}. Cold and wet; health worsened by 10."`, `phase_id = "leg_d_event_weather_pushed_on_{{ world.day }}"`, `thread = "{{ run.id }}"`, `title = "Pushed on through the weather"`, `transport = "tui"` · set `current_event_attempts = 0`, `event_kind = ""`, `health_avg = "{{ world.health_avg - 10 }}"`, `weather_kind = ""` · say "The party pushes on through the weather." |
| 3 | [`quit`](#intent-quit) |  | [`ended_lost`](#room-ended-lost) | say "The party gives up in the {{ world.weather_kind }}." |
| 4 | [`wait_out`](#intent-wait-out) |  | `../traveling` | invoke `host.transport.post` with `body = "Day {{ world.day }} — sheltered until the {{ world.weather_kind }} let up. {{ world.weather_kind == 'heavy_rain' ? '20 lbs of food spoiled in the wet.' : 'No supplies lost — only days.' }}"`, `phase_id = "leg_d_event_weather_waited_{{ world.day }}"`, `thread = "{{ run.id }}"`, `title = "Weather passed"`, `transport = "tui"` · set `current_event_attempts = 0`, `day = "{{ world.day + (world.rng_last % 3) + 1 }}"`, `event_kind = ""`, `food_lbs = "{{ world.weather_kind == 'heavy_rain' ? world.food_lbs - 20 : world.food_lbs }}"`, `weather_kind = ""` · say "The party shelters until the weather passes." |

### <a id="room-leg-d-executing-traveling"></a> `leg_d_executing.traveling`

Travelling — Chimney Rock → Fort Laramie.

**Transitions**:

| # | Intent | Guard | → | Effects |
|---|---|---|---|---|
| 1 | [`continue`](#intent-continue) | `world.miles_traveled + int((world.pace == 'grueling' ? 22 : (world.pace == 'strenuous' ? 18 : 14)) * (world.month == 'december' \|\| world.month == 'january' \|\| world.month == 'february' ? 70 : (world.month == 'november' ? 80 : (world.month == 'march' \|\| world.month == 'october' ? 85 : (world.month == 'april' \|\| world.month == 'september' ? 95 : 100)))) / 100) >= 86` | [`leg_d_awaiting_reply`](#room-leg-d-awaiting-reply) | set `day = "{{ world.day + 1 > 30 ? 1 : world.day + 1 }}"`, `food_lbs = "{{ world.food_lbs - (world.rations == 'bare_bones' ? 6 : (world.rations == 'meager' ? 8 : 10)) }}"`, `miles_traveled = "{{ world.miles_traveled + int((world.pace == 'grueling' ? 22 : (world.pace == 'strenuous' ? 18 : 14)) * (world.month == 'december' || world.month == 'january' || world.month == 'february' ? 70 : (world.month == 'november' ? 80 : (world.month == 'march' || world.month == 'october' ? 85 : (world.month == 'april' || world.month == 'september' ? 95 : 100)))) / 100) }}"`, `year = "{{ world.day + 1 > 30 && world.month == 'december' ? world.year + 1 : world.year }}"` · set `month = "{{ world.day + 1 > 30 ? (world.month == 'march' ? 'april' : (world.month == 'april' ? 'may' : (world.month == 'may' ? 'june' : (world.month == 'june' ? 'july' : (world.month == 'july' ? 'august' : (world.month == 'august' ? 'september' : (world.month == 'september' ? 'october' : (world.month == 'october' ? 'november' : (world.month == 'november' ? 'december' : (world.month == 'december' ? 'january' : (world.month == 'january' ? 'february' : (world.month == 'february' ? 'march' : world.month)))))))))))) : world.month }}"` · say "Arrived at Fort Laramie." |
| 2 | [`continue`](#intent-continue) | `world.food_lbs - (world.rations == 'bare_bones' ? 6 : (world.rations == 'meager' ? 8 : 10)) <= 0` | [`ended_lost`](#room-ended-lost) | set `day = "{{ world.day + 1 > 30 ? 1 : world.day + 1 }}"`, `food_lbs = 0`, `year = "{{ world.day + 1 > 30 && world.month == 'december' ? world.year + 1 : world.year }}"` · set `month = "{{ world.day + 1 > 30 ? (world.month == 'march' ? 'april' : (world.month == 'april' ? 'may' : (world.month == 'may' ? 'june' : (world.month == 'june' ? 'july' : (world.month == 'july' ? 'august' : (world.month == 'august' ? 'september' : (world.month == 'september' ? 'october' : (world.month == 'october' ? 'november' : (world.month == 'november' ? 'december' : (world.month == 'december' ? 'january' : (world.month == 'january' ? 'february' : (world.month == 'february' ? 'march' : world.month)))))))))))) : world.month }}"` · say "The party has run out of food between Chimney Rock and Fort Laramie. They starve on the trail." |
| 3 | [`continue`](#intent-continue) | `int(world.miles_traveled + world.rng_counter) % 100 >= 60 && int(world.miles_traveled + world.rng_counter) % 100 < 75` | `../event_disease` | set `rng_last = "{{ int(world.miles_traveled + world.rng_counter) % 100 }}"` · set `day = "{{ world.day + 1 > 30 ? 1 : world.day + 1 }}"`, `event_kind = "disease"`, `food_lbs = "{{ world.food_lbs - (world.rations == 'bare_bones' ? 6 : (world.rations == 'meager' ? 8 : 10)) }}"`, `miles_traveled = "{{ world.miles_traveled + int((world.pace == 'grueling' ? 22 : (world.pace == 'strenuous' ? 18 : 14)) * (world.month == 'december' || world.month == 'january' || world.month == 'february' ? 70 : (world.month == 'november' ? 80 : (world.month == 'march' || world.month == 'october' ? 85 : (world.month == 'april' || world.month == 'september' ? 95 : 100)))) / 100) }}"`, `year = "{{ world.day + 1 > 30 && world.month == 'december' ? world.year + 1 : world.year }}"` · set `month = "{{ world.day + 1 > 30 ? (world.month == 'march' ? 'april' : (world.month == 'april' ? 'may' : (world.month == 'may' ? 'june' : (world.month == 'june' ? 'july' : (world.month == 'july' ? 'august' : (world.month == 'august' ? 'september' : (world.month == 'september' ? 'october' : (world.month == 'october' ? 'november' : (world.month == 'november' ? 'december' : (world.month == 'december' ? 'january' : (world.month == 'january' ? 'february' : (world.month == 'february' ? 'march' : world.month)))))))))))) : world.month }}"` · increment `rng_counter += 1` |
| 4 | [`continue`](#intent-continue) | `int(world.miles_traveled + world.rng_counter) % 100 >= 75 && int(world.miles_traveled + world.rng_counter) % 100 < 85` | `../event_breakdown` | set `rng_last = "{{ int(world.miles_traveled + world.rng_counter) % 100 }}"` · set `day = "{{ world.day + 1 > 30 ? 1 : world.day + 1 }}"`, `event_kind = "breakdown"`, `food_lbs = "{{ world.food_lbs - (world.rations == 'bare_bones' ? 6 : (world.rations == 'meager' ? 8 : 10)) }}"`, `miles_traveled = "{{ world.miles_traveled + int((world.pace == 'grueling' ? 22 : (world.pace == 'strenuous' ? 18 : 14)) * (world.month == 'december' || world.month == 'january' || world.month == 'february' ? 70 : (world.month == 'november' ? 80 : (world.month == 'march' || world.month == 'october' ? 85 : (world.month == 'april' || world.month == 'september' ? 95 : 100)))) / 100) }}"`, `year = "{{ world.day + 1 > 30 && world.month == 'december' ? world.year + 1 : world.year }}"` · set `month = "{{ world.day + 1 > 30 ? (world.month == 'march' ? 'april' : (world.month == 'april' ? 'may' : (world.month == 'may' ? 'june' : (world.month == 'june' ? 'july' : (world.month == 'july' ? 'august' : (world.month == 'august' ? 'september' : (world.month == 'september' ? 'october' : (world.month == 'october' ? 'november' : (world.month == 'november' ? 'december' : (world.month == 'december' ? 'january' : (world.month == 'january' ? 'february' : (world.month == 'february' ? 'march' : world.month)))))))))))) : world.month }}"` · increment `rng_counter += 1` |
| 5 | [`continue`](#intent-continue) | `int(world.miles_traveled + world.rng_counter) % 100 >= 85 && int(world.miles_traveled + world.rng_counter) % 100 < 92` | `../event_weather` | set `rng_last = "{{ int(world.miles_traveled + world.rng_counter) % 100 }}"` · set `day = "{{ world.day + 1 > 30 ? 1 : world.day + 1 }}"`, `event_kind = "weather"`, `food_lbs = "{{ world.food_lbs - (world.rations == 'bare_bones' ? 6 : (world.rations == 'meager' ? 8 : 10)) }}"`, `miles_traveled = "{{ world.miles_traveled + int((world.pace == 'grueling' ? 22 : (world.pace == 'strenuous' ? 18 : 14)) * (world.month == 'december' || world.month == 'january' || world.month == 'february' ? 70 : (world.month == 'november' ? 80 : (world.month == 'march' || world.month == 'october' ? 85 : (world.month == 'april' || world.month == 'september' ? 95 : 100)))) / 100) }}"`, `year = "{{ world.day + 1 > 30 && world.month == 'december' ? world.year + 1 : world.year }}"` · set `month = "{{ world.day + 1 > 30 ? (world.month == 'march' ? 'april' : (world.month == 'april' ? 'may' : (world.month == 'may' ? 'june' : (world.month == 'june' ? 'july' : (world.month == 'july' ? 'august' : (world.month == 'august' ? 'september' : (world.month == 'september' ? 'october' : (world.month == 'october' ? 'november' : (world.month == 'november' ? 'december' : (world.month == 'december' ? 'january' : (world.month == 'january' ? 'february' : (world.month == 'february' ? 'march' : world.month)))))))))))) : world.month }}"` · increment `rng_counter += 1` |
| 6 | [`continue`](#intent-continue) | `int(world.miles_traveled + world.rng_counter) % 100 >= 92 && int(world.miles_traveled + world.rng_counter) % 100 < 97` | `../event_encounter` | set `rng_last = "{{ int(world.miles_traveled + world.rng_counter) % 100 }}"` · set `day = "{{ world.day + 1 > 30 ? 1 : world.day + 1 }}"`, `event_kind = "encounter"`, `food_lbs = "{{ world.food_lbs - (world.rations == 'bare_bones' ? 6 : (world.rations == 'meager' ? 8 : 10)) }}"`, `miles_traveled = "{{ world.miles_traveled + int((world.pace == 'grueling' ? 22 : (world.pace == 'strenuous' ? 18 : 14)) * (world.month == 'december' || world.month == 'january' || world.month == 'february' ? 70 : (world.month == 'november' ? 80 : (world.month == 'march' || world.month == 'october' ? 85 : (world.month == 'april' || world.month == 'september' ? 95 : 100)))) / 100) }}"`, `year = "{{ world.day + 1 > 30 && world.month == 'december' ? world.year + 1 : world.year }}"` · set `month = "{{ world.day + 1 > 30 ? (world.month == 'march' ? 'april' : (world.month == 'april' ? 'may' : (world.month == 'may' ? 'june' : (world.month == 'june' ? 'july' : (world.month == 'july' ? 'august' : (world.month == 'august' ? 'september' : (world.month == 'september' ? 'october' : (world.month == 'october' ? 'november' : (world.month == 'november' ? 'december' : (world.month == 'december' ? 'january' : (world.month == 'january' ? 'february' : (world.month == 'february' ? 'march' : world.month)))))))))))) : world.month }}"` · increment `rng_counter += 1` |
| 7 | [`continue`](#intent-continue) | `int(world.miles_traveled + world.rng_counter) % 100 >= 97` | `../event_supply_loss` | set `rng_last = "{{ int(world.miles_traveled + world.rng_counter) % 100 }}"` · set `day = "{{ world.day + 1 > 30 ? 1 : world.day + 1 }}"`, `event_kind = "supply_loss"`, `food_lbs = "{{ world.food_lbs - (world.rations == 'bare_bones' ? 6 : (world.rations == 'meager' ? 8 : 10)) }}"`, `miles_traveled = "{{ world.miles_traveled + int((world.pace == 'grueling' ? 22 : (world.pace == 'strenuous' ? 18 : 14)) * (world.month == 'december' || world.month == 'january' || world.month == 'february' ? 70 : (world.month == 'november' ? 80 : (world.month == 'march' || world.month == 'october' ? 85 : (world.month == 'april' || world.month == 'september' ? 95 : 100)))) / 100) }}"`, `year = "{{ world.day + 1 > 30 && world.month == 'december' ? world.year + 1 : world.year }}"` · set `month = "{{ world.day + 1 > 30 ? (world.month == 'march' ? 'april' : (world.month == 'april' ? 'may' : (world.month == 'may' ? 'june' : (world.month == 'june' ? 'july' : (world.month == 'july' ? 'august' : (world.month == 'august' ? 'september' : (world.month == 'september' ? 'october' : (world.month == 'october' ? 'november' : (world.month == 'november' ? 'december' : (world.month == 'december' ? 'january' : (world.month == 'january' ? 'february' : (world.month == 'february' ? 'march' : world.month)))))))))))) : world.month }}"` · increment `rng_counter += 1` |
| 8 | [`continue`](#intent-continue) | _default_ | `.` | set `rng_last = "{{ int(world.miles_traveled + world.rng_counter) % 100 }}"` · set `day = "{{ world.day + 1 > 30 ? 1 : world.day + 1 }}"`, `event_kind = ""`, `food_lbs = "{{ world.food_lbs - (world.rations == 'bare_bones' ? 6 : (world.rations == 'meager' ? 8 : 10)) }}"`, `miles_traveled = "{{ world.miles_traveled + int((world.pace == 'grueling' ? 22 : (world.pace == 'strenuous' ? 18 : 14)) * (world.month == 'december' || world.month == 'january' || world.month == 'february' ? 70 : (world.month == 'november' ? 80 : (world.month == 'march' || world.month == 'october' ? 85 : (world.month == 'april' || world.month == 'september' ? 95 : 100)))) / 100) }}"`, `year = "{{ world.day + 1 > 30 && world.month == 'december' ? world.year + 1 : world.year }}"` · set `month = "{{ world.day + 1 > 30 ? (world.month == 'march' ? 'april' : (world.month == 'april' ? 'may' : (world.month == 'may' ? 'june' : (world.month == 'june' ? 'july' : (world.month == 'july' ? 'august' : (world.month == 'august' ? 'september' : (world.month == 'september' ? 'october' : (world.month == 'october' ? 'november' : (world.month == 'november' ? 'december' : (world.month == 'december' ? 'january' : (world.month == 'january' ? 'february' : (world.month == 'february' ? 'march' : world.month)))))))))))) : world.month }}"` · increment `rng_counter += 1` |
| 9 | [`look`](#intent-look) |  | `.` |  |
| 10 | [`quit`](#intent-quit) |  | [`ended_lost`](#room-ended-lost) | say "The party abandons the journey between Chimney Rock and Fort Laramie." |
| 11 | [`set_pace`](#intent-set-pace) |  | `.` | set `pace = "{{ slots.pace }}"` · say "Pace set to {{ slots.pace }}." |
| 12 | [`set_rations`](#intent-set-rations) |  | `.` | set `rations = "{{ slots.rations }}"` · say "Rations set to {{ slots.rations }}." |

### <a id="room-leg-e-awaiting-reply"></a> `leg_e_awaiting_reply`

Arrived at South Pass (mountain).

**Shows world**: `day`, `miles_traveled`, `food_lbs`, `oxen`, `party_alive`, `health_avg`, `illness_kind`, `illness_severity`, `illness_member`, `last_landmark_prose`

**On enter**:

1. set `last_landmark_prose = ""`
2. invoke `host.transport.post` with `body = "Day {{ world.day }}, {{ world.month }} {{ world.year }}. We rolled into **South Pass** (mountain) at last.\n\n- Food: {{ world.food_lbs }} lbs\n- Oxen: {{ world.oxen }}\n- Party: {{ world.party_alive }} alive\n- Health: {{ world.health_avg }}\n"`, `phase_id = "leg_e_arrival"`, `thread = "{{ run.id }}"`, `title = "Day {{ world.day }}: South Pass"`, `transport = "tui"`
3. invoke `host.agent.ask` with `agent = "trail_narrator"`, `args = map[day:{{ world.day }} food_lbs:{{ world.food_lbs }} landmark:South Pass miles_traveled:{{ world.miles_traveled }} month:{{ world.month }} party_alive:{{ world.party_alive }} year:{{ world.year }}]`, `prompt_path = "prompts/landmark_arrival.md"`, bind `last_landmark_prose ← stdout`

**Transitions**:

| # | Intent | Guard | → | Effects |
|---|---|---|---|---|
| 1 | [`approach_river`](#intent-approach-river) | `false` | [`river_crossing`](#room-river-crossing) | set `current_landmark = "South Pass"`, `river_depth_ft = "{{ int(0 * (world.month == 'april' ? 160 : (world.month == 'march' ? 140 : (world.month == 'may' ? 130 : (world.month == 'june' ? 100 : (world.month == 'july' ? 80 : (world.month == 'august' ? 70 : (world.month == 'september' ? 80 : 100))))))) / 100) }}"`, `river_width_ft = "{{ int(0) }}"` |
| 2 | [`approach_river`](#intent-approach-river) | _default_ | `.` | _hint: No river at this landmark._ |
| 3 | [`consult_guide`](#intent-consult-guide) |  | [`trail_guide`](#room-trail-guide) | set `last_job_originating_state = "leg_e_awaiting_reply"` · _(no-history)_ |
| 4 | [`continue`](#intent-continue) | `'South Pass' == 'South Pass' && (world.month == 'october' \|\| world.month == 'november' \|\| world.month == 'december' \|\| world.month == 'january' \|\| world.month == 'february')` | [`snow_blocked`](#room-snow-blocked) | set `current_landmark = "South Pass"` · say "South Pass is snowed in. The wagons cannot get through." |
| 5 | [`continue`](#intent-continue) | _default_ | [`leg_f_executing`](#room-leg-f-executing) | set `breakdown_part = ""`, `current_event_attempts = 0`, `current_landmark = "South Pass"`, `encounter_kind = ""`, `event_kind = ""`, `illness_kind = ""`, `miles_traveled = 0`, `weather_kind = ""` |
| 6 | [`enter_fort`](#intent-enter-fort) | `false` | [`fort`](#room-fort) | set `current_landmark = "South Pass"` |
| 7 | [`enter_fort`](#intent-enter-fort) | _default_ | `.` | _hint: No fort at this landmark._ |
| 8 | [`face_robbery`](#intent-face-robbery) |  | [`frontier`](#room-frontier) |  |
| 9 | [`give_up_leg`](#intent-give-up-leg) | `world.cycle__leg_e__on_failure < 2` | [`leg_d_executing`](#room-leg-d-executing) | increment `cycle__leg_e__on_failure += 1` · set `breakdown_part = ""`, `current_event_attempts = 0`, `encounter_kind = ""`, `event_kind = ""`, `illness_kind = ""`, `miles_traveled = 0`, `weather_kind = ""` · say "The party turns back toward Fort Laramie." |
| 10 | [`give_up_leg`](#intent-give-up-leg) | _default_ | [`leg_e_error`](#room-leg-e-error) | say "The party has given up too many times — stranded." |
| 11 | [`hunt`](#intent-hunt) |  | [`hunt`](#room-hunt) |  |
| 12 | [`quit`](#intent-quit) |  | [`ended_lost`](#room-ended-lost) | say "The party abandons the journey at South Pass." |
| 13 | [`rest`](#intent-rest) |  | [`rest_room`](#room-rest-room) |  |
| 14 | [`restart_from`](#intent-restart-from) | `slots.stage == 'independence' \|\| slots.stage == 'kansas'` | [`leg_a_executing`](#room-leg-a-executing) | set `breakdown_part = ""`, `current_event_attempts = 0`, `encounter_kind = ""`, `event_kind = ""`, `illness_kind = ""`, `miles_traveled = 0`, `weather_kind = ""` · say "The party doubles back to Independence to retry the run toward Kansas River." |
| 15 | [`restart_from`](#intent-restart-from) | `slots.stage == 'kearney'` | [`leg_b_executing`](#room-leg-b-executing) | set `breakdown_part = ""`, `current_event_attempts = 0`, `encounter_kind = ""`, `event_kind = ""`, `illness_kind = ""`, `miles_traveled = 0`, `weather_kind = ""` · say "The party doubles back to Kansas River to retry the stretch toward Fort Kearney." |
| 16 | [`restart_from`](#intent-restart-from) | `slots.stage == 'chimney'` | [`leg_c_executing`](#room-leg-c-executing) | set `breakdown_part = ""`, `current_event_attempts = 0`, `encounter_kind = ""`, `event_kind = ""`, `illness_kind = ""`, `miles_traveled = 0`, `weather_kind = ""` · say "The party doubles back to Fort Kearney to retry the stretch toward Chimney Rock." |
| 17 | [`restart_from`](#intent-restart-from) | `slots.stage == 'laramie'` | [`leg_d_executing`](#room-leg-d-executing) | set `breakdown_part = ""`, `current_event_attempts = 0`, `encounter_kind = ""`, `event_kind = ""`, `illness_kind = ""`, `miles_traveled = 0`, `weather_kind = ""` · say "The party doubles back to Chimney Rock to retry the stretch toward Fort Laramie." |
| 18 | [`restart_from`](#intent-restart-from) | `slots.stage == 'south_pass'` | [`leg_e_executing`](#room-leg-e-executing) | set `breakdown_part = ""`, `current_event_attempts = 0`, `encounter_kind = ""`, `event_kind = ""`, `illness_kind = ""`, `miles_traveled = 0`, `weather_kind = ""` · say "The party doubles back to Fort Laramie to retry the stretch toward South Pass." |
| 19 | [`restart_from`](#intent-restart-from) | `slots.stage == 'snake'` | [`leg_f_executing`](#room-leg-f-executing) | set `breakdown_part = ""`, `current_event_attempts = 0`, `encounter_kind = ""`, `event_kind = ""`, `illness_kind = ""`, `miles_traveled = 0`, `weather_kind = ""` · say "The party doubles back to South Pass to retry the stretch toward Snake River." |
| 20 | [`restart_from`](#intent-restart-from) | _default_ | `.` | _hint: Unknown restart stage._ |
| 21 | [`scout`](#intent-scout) |  | [`frontier`](#room-frontier) |  |

**Timeout**: after `10d` → `leg_f_executing`

### <a id="room-leg-e-error"></a> `leg_e_error`

Stranded between Fort Laramie and South Pass.

**Shows world**: `day`, `party_alive`, `current_landmark`

**Transitions**:

| # | Intent | Guard | → | Effects |
|---|---|---|---|---|
| 1 | [`quit`](#intent-quit) |  | [`ended_lost`](#room-ended-lost) | say "The party gives up between Fort Laramie and South Pass." |
| 2 | [`retry`](#intent-retry) |  | [`leg_e_executing`](#room-leg-e-executing) | set `current_event_attempts = 0`, `event_kind = ""` |

### <a id="room-leg-e-executing"></a> `leg_e_executing`  _(compound)_

On the trail from Fort Laramie to South Pass (mountain).

**Initial child**: `traveling`

**Shows world**: `day`, `month`, `miles_traveled`, `food_lbs`, `oxen`, `party_alive`, `health_avg`, `pace`, `rations`, `current_landmark`, `event_kind`, `illness_kind`, `illness_severity`, `illness_member`, `breakdown_part`, `weather_kind`, `encounter_kind`, `rng_last`, `rng_counter`

**On enter**:

1. set `current_landmark = "Fort Laramie"`

**Transitions**:

| # | Intent | Guard | → | Effects |
|---|---|---|---|---|
| 1 | [`on_failure`](#intent-on-failure) | `world.cycle__leg_e__on_failure < 2` | [`leg_d_executing`](#room-leg-d-executing) | increment `cycle__leg_e__on_failure += 1` |
| 2 | [`on_failure`](#intent-on-failure) | _default_ | [`leg_e_error`](#room-leg-e-error) | _hint: cycle budget exceeded for on_failure_ |

### <a id="room-leg-e-executing-event-breakdown"></a> `leg_e_executing.event_breakdown`

Wagon breakdown — {{ world.breakdown_part }}.

**Shows world**: `breakdown_part`, `spare_wheels`, `spare_axles`, `spare_tongues`, `day`, `current_event_attempts`, `last_event_prose`

**On enter**:

1. set `breakdown_part = "{{ if world.rng_last % 3 == 0 }}wheel{{ else }}{{ if world.rng_last % 3 == 1 }}axle{{ else }}tongue{{ end }}{{ end }}"`, `current_event_attempts = 0`, `last_event_prose = ""`
2. invoke `host.agent.ask` with `agent = "trail_narrator"`, `args = map[current_landmark:{{ world.current_landmark }} day:{{ world.day }} part:{{ if world.rng_last % 3 == 0 }}wheel{{ else }}{{ if world.rng_last % 3 == 1 }}axle{{ else }}tongue{{ end }}{{ end }} spares_remaining:{{ world.rng_last % 3 == 0 ? world.spare_wheels : (world.rng_last % 3 == 1 ? world.spare_axles : world.spare_tongues) }}]`, `prompt_path = "prompts/event_breakdown.md"`, bind `last_event_prose ← stdout`

**Transitions**:

| # | Intent | Guard | → | Effects |
|---|---|---|---|---|
| 1 | [`look`](#intent-look) |  | `.` |  |
| 2 | [`quit`](#intent-quit) |  | [`ended_lost`](#room-ended-lost) | say "The party abandons the broken wagon." |
| 3 | [`repair`](#intent-repair) | `world.breakdown_part == 'wheel' && world.spare_wheels >= 1` | `../traveling` | invoke `host.transport.post` with `body = "Day {{ world.day }} — fitted the spare wheel and got the wagon rolling again. One less in reserve."`, `phase_id = "leg_e_event_breakdown_wheel_repaired_{{ world.day }}"`, `thread = "{{ run.id }}"`, `title = "Wheel repaired"`, `transport = "tui"` · set `breakdown_part = ""`, `current_event_attempts = 0`, `event_kind = ""`, `spare_wheels = "{{ world.spare_wheels - 1 }}"` · say "Spare wheel installed. Back on the trail." |
| 4 | [`repair`](#intent-repair) | `world.breakdown_part == 'axle' && world.spare_axles >= 1` | `../traveling` | invoke `host.transport.post` with `body = "Day {{ world.day }} — swapped in the spare axle. Took the morning, but the wagon's true again."`, `phase_id = "leg_e_event_breakdown_axle_repaired_{{ world.day }}"`, `thread = "{{ run.id }}"`, `title = "Axle repaired"`, `transport = "tui"` · set `breakdown_part = ""`, `current_event_attempts = 0`, `event_kind = ""`, `spare_axles = "{{ world.spare_axles - 1 }}"` · say "Spare axle installed. Back on the trail." |
| 5 | [`repair`](#intent-repair) | `world.breakdown_part == 'tongue' && world.spare_tongues >= 1` | `../traveling` | invoke `host.transport.post` with `body = "Day {{ world.day }} — pinned in the spare tongue and we hitched the oxen back up."`, `phase_id = "leg_e_event_breakdown_tongue_repaired_{{ world.day }}"`, `thread = "{{ run.id }}"`, `title = "Tongue repaired"`, `transport = "tui"` · set `breakdown_part = ""`, `current_event_attempts = 0`, `event_kind = ""`, `spare_tongues = "{{ world.spare_tongues - 1 }}"` · say "Spare tongue installed. Back on the trail." |
| 6 | [`repair`](#intent-repair) | `world.current_event_attempts < 2` | `.` | _hint: Need a spare {{ world.breakdown_part }} to repair._ · increment `current_event_attempts += 1` · say "No spare {{ world.breakdown_part }} on hand. Try repair again, wait_out, or look." |
| 7 | [`repair`](#intent-repair) | _default_ | `../traveling` | invoke `host.transport.post` with `body = "Day {{ world.day }} — couldn't repair the {{ world.breakdown_part }}. Lashed it together and pressed on at a cost: a member of the party was left behind."`, `phase_id = "leg_e_event_breakdown_failed_{{ world.day }}"`, `thread = "{{ run.id }}"`, `title = "Wagon limping on"`, `transport = "tui"` · set `breakdown_part = ""`, `current_event_attempts = 0`, `event_kind = ""`, `health_avg = "{{ world.health_avg - 20 }}"`, `party_alive = "{{ world.party_alive - 1 }}"` · say "Repeated repair attempts failed; the wagon is patched poorly and the party limps on. A member is left behind." |
| 8 | [`wait_out`](#intent-wait-out) |  | `../traveling` | invoke `host.transport.post` with `body = "Day {{ world.day }} — made camp and improvised a {{ world.breakdown_part }} repair over five days. Slow going, but back on the trail."`, `phase_id = "leg_e_event_breakdown_waited_{{ world.day }}"`, `thread = "{{ run.id }}"`, `title = "Improvised repair"`, `transport = "tui"` · set `breakdown_part = ""`, `current_event_attempts = 0`, `day = "{{ world.day + 5 }}"`, `event_kind = ""` · say "The party makes camp and improvises a repair over five days." |

### <a id="room-leg-e-executing-event-disease"></a> `leg_e_executing.event_disease`

Illness has struck the party ({{ world.illness_kind }}).

**Shows world**: `illness_kind`, `illness_severity`, `illness_treatment`, `illness_member`, `health_avg`, `party_alive`, `food_lbs`, `clothing_sets`, `day`, `current_event_attempts`

**On enter**:

1. set `current_event_attempts = 0`, `health_avg = "{{ world.health_avg - 10 }}"`, `illness_member = "{{ split(world.party_names, ',')[world.rng_last % world.party_alive] }}"`
2. set `illness_kind = "{{ if world.rng_last % 5 == 0 }}dysentery{{ else }}{{ if world.rng_last % 5 == 1 }}cholera{{ else }}{{ if world.rng_last % 5 == 2 }}typhoid{{ else }}{{ if world.rng_last % 5 == 3 }}measles{{ else }}exhaustion{{ end }}{{ end }}{{ end }}{{ end }}"`, `illness_severity = "{{ world.rng_last % 5 + 1 }}"`, `illness_treatment = "rest"`
3. invoke `host.agent.decide` with `agent = "frontier_doctor"`, `args = map[clothing_sets:{{ world.clothing_sets }} current_landmark:{{ world.current_landmark }} food_lbs:{{ world.food_lbs }} health_avg:{{ world.health_avg }} party_alive:{{ world.party_alive }} rng_last:{{ world.rng_last }}]`, `prompt = "prompts/event_disease.md"`, `schema = "mcp/illness.json"`, bind `illness_kind ← submitted.illness`, `illness_severity ← submitted.severity`, `illness_treatment ← submitted.treatment`, on_error → `leg_e_error`

**Transitions**:

| # | Intent | Guard | → | Effects |
|---|---|---|---|---|
| 1 | [`look`](#intent-look) |  | `.` |  |
| 2 | [`move_on`](#intent-move-on) |  | `../traveling` | invoke `host.transport.post` with `body = "Day {{ world.day }} — pushed on through the {{ world.illness_kind }} rather than stop. Health is worse for it, but the wagon rolls."`, `phase_id = "leg_e_event_disease_moved_on_{{ world.day }}"`, `thread = "{{ run.id }}"`, `title = "Pressing on through illness"`, `transport = "tui"` · set `current_event_attempts = 0`, `event_kind = ""`, `health_avg = "{{ world.health_avg - 15 }}"`, `illness_kind = ""`, `illness_member = ""`, `illness_severity = 0`, `illness_treatment = ""` · say "The party presses on. Health worsens." |
| 3 | [`quit`](#intent-quit) |  | [`ended_lost`](#room-ended-lost) | say "The party gives up on the trail, broken by {{ world.illness_kind }}." |
| 4 | [`treat`](#intent-treat) | `world.clothing_sets >= 1 && world.food_lbs >= 50 && world.current_event_attempts < 2` | `../traveling` | invoke `host.transport.post` with `body = "Day {{ world.day }} — the {{ world.illness_kind }} has passed. Rested up, one clothing set used and 50 lbs of food spent on broth and care. Spirits steady, on the move."`, `phase_id = "leg_e_event_disease_treated_{{ world.day }}"`, `thread = "{{ run.id }}"`, `title = "Disease treated"`, `transport = "tui"` · set `clothing_sets = "{{ world.clothing_sets - 1 }}"`, `current_event_attempts = 0`, `event_kind = ""`, `food_lbs = "{{ world.food_lbs - 50 }}"`, `health_avg = "{{ world.health_avg + 20 }}"`, `illness_kind = ""`, `illness_member = ""`, `illness_severity = 0`, `illness_treatment = ""` · say "The party rests and treats the illness." |
| 5 | [`treat`](#intent-treat) | `(world.clothing_sets < 1 \|\| world.food_lbs < 50) && world.current_event_attempts < 2` | `.` | increment `current_event_attempts += 1` · say "Not enough supplies to treat the {{ world.illness_kind }} (need 1 clothing set + 50 lbs food). Try again or wait_out." |
| 6 | [`treat`](#intent-treat) | _default_ | `../traveling` | invoke `host.transport.post` with `body = "Day {{ world.day }} — after repeated attempts, the {{ world.illness_kind }} took one of the party. May the trail remember them."`, `phase_id = "leg_e_event_disease_lost_{{ world.day }}"`, `thread = "{{ run.id }}"`, `title = "Lost to illness"`, `transport = "tui"` · set `current_event_attempts = 0`, `event_kind = ""`, `illness_kind = ""`, `illness_member = ""`, `illness_severity = 0`, `illness_treatment = ""`, `party_alive = "{{ world.party_alive - 1 }}"` · say "After repeated futile attempts, a party member has died of illness." |
| 7 | [`wait_out`](#intent-wait-out) | `world.health_avg < 30` | `../traveling` | invoke `host.transport.post` with `body = "Day {{ world.day }} — the patient was too weak. {{ world.illness_kind }} claimed one of the party while we waited."`, `phase_id = "leg_e_event_disease_wait_lost_{{ world.day }}"`, `thread = "{{ run.id }}"`, `title = "Lost to illness"`, `transport = "tui"` · set `current_event_attempts = 0`, `day = "{{ world.day + 1 }}"`, `event_kind = ""`, `illness_kind = ""`, `illness_member = ""`, `illness_severity = 0`, `illness_treatment = ""`, `party_alive = "{{ world.party_alive - 1 }}"` · say "The patient was too weak. A party member dies of illness." |
| 8 | [`wait_out`](#intent-wait-out) | _default_ | `../traveling` | invoke `host.transport.post` with `body = "Day {{ world.day }} — one day of rest and the {{ world.illness_kind }} let go. We move on tomorrow."`, `phase_id = "leg_e_event_disease_wait_ok_{{ world.day }}"`, `thread = "{{ run.id }}"`, `title = "Illness passed"`, `transport = "tui"` · set `current_event_attempts = 0`, `day = "{{ world.day + 1 }}"`, `event_kind = ""`, `illness_kind = ""`, `illness_member = ""`, `illness_severity = 0`, `illness_treatment = ""` · say "The party rests a day. The illness passes." |

### <a id="room-leg-e-executing-event-encounter"></a> `leg_e_executing.event_encounter`

Encounter on the trail: {{ world.encounter_kind }}.

**Shows world**: `encounter_kind`, `food_lbs`, `clothing_sets`, `last_event_prose`

**On enter**:

1. set `current_event_attempts = 0`, `encounter_kind = "{{ if world.rng_last % 3 == 0 }}trader{{ else }}{{ if world.rng_last % 3 == 1 }}hunter{{ else }}band{{ end }}{{ end }}"`, `last_event_prose = ""`
2. invoke `host.agent.ask` with `agent = "trail_narrator"`, `args = map[clothing_sets:{{ world.clothing_sets }} current_landmark:{{ world.current_landmark }} day:{{ world.day }} food_lbs:{{ world.food_lbs }} kind:{{ if world.rng_last % 3 == 0 }}trader{{ else }}{{ if world.rng_last % 3 == 1 }}hunter{{ else }}band{{ end }}{{ end }}]`, `prompt_path = "prompts/event_encounter.md"`, bind `last_event_prose ← stdout`

**Transitions**:

| # | Intent | Guard | → | Effects |
|---|---|---|---|---|
| 1 | [`accept_trade`](#intent-accept-trade) | `world.food_lbs >= 50` | `../traveling` | invoke `host.transport.post` with `body = "Day {{ world.day }} — traded 50 lbs of food to a {{ world.encounter_kind }} for one clothing set. A fair deal on the trail."`, `phase_id = "leg_e_event_encounter_traded_{{ world.day }}"`, `thread = "{{ run.id }}"`, `title = "Trade with a {{ world.encounter_kind }}"`, `transport = "tui"` · set `clothing_sets = "{{ world.clothing_sets + 1 }}"`, `current_event_attempts = 0`, `encounter_kind = ""`, `event_kind = ""`, `food_lbs = "{{ world.food_lbs - 50 }}"` · say "Trade complete: -50 lbs food, +1 clothing set." |
| 2 | [`accept_trade`](#intent-accept-trade) | _default_ | `.` | _hint: Not enough food to trade._ |
| 3 | [`decline_trade`](#intent-decline-trade) |  | `../traveling` | invoke `host.transport.post` with `body = "Day {{ world.day }} — passed on a trade with a {{ world.encounter_kind }}. The wagon kept rolling."`, `phase_id = "leg_e_event_encounter_declined_{{ world.day }}"`, `thread = "{{ run.id }}"`, `title = "Declined a trade"`, `transport = "tui"` · set `current_event_attempts = 0`, `encounter_kind = ""`, `event_kind = ""` · say "The party declines the trade and presses on." |
| 4 | [`look`](#intent-look) |  | `.` |  |
| 5 | [`move_on`](#intent-move-on) |  | `../traveling` | invoke `host.transport.post` with `body = "Day {{ world.day }} — moved on past the {{ world.encounter_kind }}. No words exchanged."`, `phase_id = "leg_e_event_encounter_moved_on_{{ world.day }}"`, `thread = "{{ run.id }}"`, `title = "Moved on from a {{ world.encounter_kind }}"`, `transport = "tui"` · set `current_event_attempts = 0`, `encounter_kind = ""`, `event_kind = ""` · say "The party moves on past the encounter." |
| 6 | [`quit`](#intent-quit) |  | [`ended_lost`](#room-ended-lost) | say "The party gives up at the encounter." |

### <a id="room-leg-e-executing-event-supply-loss"></a> `leg_e_executing.event_supply_loss`

Supplies lost on the trail.

**Shows world**: `food_lbs`, `oxen`, `rng_last`, `last_event_prose`

**On enter**:

1. set `current_event_attempts = 0`, `last_event = "{{ if world.rng_last % 2 == 0 }}food_loss{{ else }}ox_loss{{ end }}"`, `last_event_prose = ""`
2. set `food_lbs = "{{ world.rng_last % 2 == 0 ? world.food_lbs - (10 + 10 * (world.rng_last % 4)) : world.food_lbs }}"`, `oxen = "{{ world.rng_last % 2 == 0 ? world.oxen : world.oxen - 1 }}"`
3. invoke `host.agent.ask` with `agent = "trail_narrator"`, `args = map[current_landmark:{{ world.current_landmark }} day:{{ world.day }} food_lbs:{{ world.food_lbs }} oxen:{{ world.oxen }} what:{{ world.rng_last % 2 == 0 ? 'food spoiled' : 'ox lame' }}]`, `prompt_path = "prompts/event_supply_loss.md"`, bind `last_event_prose ← stdout`

**Transitions**:

| # | Intent | Guard | → | Effects |
|---|---|---|---|---|
| 1 | [`look`](#intent-look) |  | `.` |  |
| 2 | [`move_on`](#intent-move-on) |  | `../traveling` | invoke `host.transport.post` with `body = "Day {{ world.day }} — {{ world.last_event == 'food_loss' ? 'food spoiled' : 'an ox went lame' }} on the trail. Recovered what we could and pressed on. Food: {{ world.food_lbs }} lbs, oxen: {{ world.oxen }}."`, `phase_id = "leg_e_event_supply_loss_recovered_{{ world.day }}"`, `thread = "{{ run.id }}"`, `title = "Supplies lost"`, `transport = "tui"` · set `current_event_attempts = 0`, `event_kind = ""`, `last_event = ""` · say "The party recovers what it can and presses on." |
| 3 | [`quit`](#intent-quit) |  | [`ended_lost`](#room-ended-lost) | say "The party gives up after losing supplies." |

### <a id="room-leg-e-executing-event-weather"></a> `leg_e_executing.event_weather`

Severe weather: {{ world.weather_kind }}.

**Shows world**: `weather_kind`, `day`, `food_lbs`, `health_avg`, `last_event_prose`

**On enter**:

1. set `current_event_attempts = 0`, `last_event_prose = ""`, `weather_kind = "{{ world.month == 'november' || world.month == 'december' || world.month == 'january' || world.month == 'february' ? 'snow' : (world.month == 'march' || world.month == 'april' || world.month == 'may' ? 'heavy_rain' : (world.month == 'june' || world.month == 'july' || world.month == 'august' ? (world.rng_last % 2 == 0 ? 'hail' : 'fog') : (world.rng_last % 2 == 0 ? 'heavy_rain' : 'fog'))) }}"`
2. invoke `host.agent.ask` with `agent = "trail_narrator"`, `args = map[current_landmark:{{ world.current_landmark }} day:{{ world.day }} kind:{{ world.month == 'november' || world.month == 'december' || world.month == 'january' || world.month == 'february' ? 'snow' : (world.month == 'march' || world.month == 'april' || world.month == 'may' ? 'heavy_rain' : (world.month == 'june' || world.month == 'july' || world.month == 'august' ? (world.rng_last % 2 == 0 ? 'hail' : 'fog') : (world.rng_last % 2 == 0 ? 'heavy_rain' : 'fog'))) }} month:{{ world.month }} terrain:mountain]`, `prompt_path = "prompts/event_weather.md"`, bind `last_event_prose ← stdout`

**Transitions**:

| # | Intent | Guard | → | Effects |
|---|---|---|---|---|
| 1 | [`look`](#intent-look) |  | `.` |  |
| 2 | [`push_on`](#intent-push-on) |  | `../traveling` | invoke `host.transport.post` with `body = "Day {{ world.day }} — pushed on through the {{ world.weather_kind }}. Cold and wet; health worsened by 10."`, `phase_id = "leg_e_event_weather_pushed_on_{{ world.day }}"`, `thread = "{{ run.id }}"`, `title = "Pushed on through the weather"`, `transport = "tui"` · set `current_event_attempts = 0`, `event_kind = ""`, `health_avg = "{{ world.health_avg - 10 }}"`, `weather_kind = ""` · say "The party pushes on through the weather." |
| 3 | [`quit`](#intent-quit) |  | [`ended_lost`](#room-ended-lost) | say "The party gives up in the {{ world.weather_kind }}." |
| 4 | [`wait_out`](#intent-wait-out) |  | `../traveling` | invoke `host.transport.post` with `body = "Day {{ world.day }} — sheltered until the {{ world.weather_kind }} let up. {{ world.weather_kind == 'heavy_rain' ? '20 lbs of food spoiled in the wet.' : 'No supplies lost — only days.' }}"`, `phase_id = "leg_e_event_weather_waited_{{ world.day }}"`, `thread = "{{ run.id }}"`, `title = "Weather passed"`, `transport = "tui"` · set `current_event_attempts = 0`, `day = "{{ world.day + (world.rng_last % 3) + 1 }}"`, `event_kind = ""`, `food_lbs = "{{ world.weather_kind == 'heavy_rain' ? world.food_lbs - 20 : world.food_lbs }}"`, `weather_kind = ""` · say "The party shelters until the weather passes." |

### <a id="room-leg-e-executing-traveling"></a> `leg_e_executing.traveling`

Travelling — Fort Laramie → South Pass.

**Transitions**:

| # | Intent | Guard | → | Effects |
|---|---|---|---|---|
| 1 | [`continue`](#intent-continue) | `world.miles_traveled + int((world.pace == 'grueling' ? 22 : (world.pace == 'strenuous' ? 18 : 14)) * (world.month == 'december' \|\| world.month == 'january' \|\| world.month == 'february' ? 70 : (world.month == 'november' ? 80 : (world.month == 'march' \|\| world.month == 'october' ? 85 : (world.month == 'april' \|\| world.month == 'september' ? 95 : 100)))) / 100) >= 292` | [`leg_e_awaiting_reply`](#room-leg-e-awaiting-reply) | set `day = "{{ world.day + 1 > 30 ? 1 : world.day + 1 }}"`, `food_lbs = "{{ world.food_lbs - (world.rations == 'bare_bones' ? 6 : (world.rations == 'meager' ? 8 : 10)) }}"`, `miles_traveled = "{{ world.miles_traveled + int((world.pace == 'grueling' ? 22 : (world.pace == 'strenuous' ? 18 : 14)) * (world.month == 'december' || world.month == 'january' || world.month == 'february' ? 70 : (world.month == 'november' ? 80 : (world.month == 'march' || world.month == 'october' ? 85 : (world.month == 'april' || world.month == 'september' ? 95 : 100)))) / 100) }}"`, `year = "{{ world.day + 1 > 30 && world.month == 'december' ? world.year + 1 : world.year }}"` · set `month = "{{ world.day + 1 > 30 ? (world.month == 'march' ? 'april' : (world.month == 'april' ? 'may' : (world.month == 'may' ? 'june' : (world.month == 'june' ? 'july' : (world.month == 'july' ? 'august' : (world.month == 'august' ? 'september' : (world.month == 'september' ? 'october' : (world.month == 'october' ? 'november' : (world.month == 'november' ? 'december' : (world.month == 'december' ? 'january' : (world.month == 'january' ? 'february' : (world.month == 'february' ? 'march' : world.month)))))))))))) : world.month }}"` · say "Arrived at South Pass." |
| 2 | [`continue`](#intent-continue) | `world.food_lbs - (world.rations == 'bare_bones' ? 6 : (world.rations == 'meager' ? 8 : 10)) <= 0` | [`ended_lost`](#room-ended-lost) | set `day = "{{ world.day + 1 > 30 ? 1 : world.day + 1 }}"`, `food_lbs = 0`, `year = "{{ world.day + 1 > 30 && world.month == 'december' ? world.year + 1 : world.year }}"` · set `month = "{{ world.day + 1 > 30 ? (world.month == 'march' ? 'april' : (world.month == 'april' ? 'may' : (world.month == 'may' ? 'june' : (world.month == 'june' ? 'july' : (world.month == 'july' ? 'august' : (world.month == 'august' ? 'september' : (world.month == 'september' ? 'october' : (world.month == 'october' ? 'november' : (world.month == 'november' ? 'december' : (world.month == 'december' ? 'january' : (world.month == 'january' ? 'february' : (world.month == 'february' ? 'march' : world.month)))))))))))) : world.month }}"` · say "The party has run out of food between Fort Laramie and South Pass. They starve on the trail." |
| 3 | [`continue`](#intent-continue) | `int(world.miles_traveled + world.rng_counter) % 100 >= 60 && int(world.miles_traveled + world.rng_counter) % 100 < 75` | `../event_disease` | set `rng_last = "{{ int(world.miles_traveled + world.rng_counter) % 100 }}"` · set `day = "{{ world.day + 1 > 30 ? 1 : world.day + 1 }}"`, `event_kind = "disease"`, `food_lbs = "{{ world.food_lbs - (world.rations == 'bare_bones' ? 6 : (world.rations == 'meager' ? 8 : 10)) }}"`, `miles_traveled = "{{ world.miles_traveled + int((world.pace == 'grueling' ? 22 : (world.pace == 'strenuous' ? 18 : 14)) * (world.month == 'december' || world.month == 'january' || world.month == 'february' ? 70 : (world.month == 'november' ? 80 : (world.month == 'march' || world.month == 'october' ? 85 : (world.month == 'april' || world.month == 'september' ? 95 : 100)))) / 100) }}"`, `year = "{{ world.day + 1 > 30 && world.month == 'december' ? world.year + 1 : world.year }}"` · set `month = "{{ world.day + 1 > 30 ? (world.month == 'march' ? 'april' : (world.month == 'april' ? 'may' : (world.month == 'may' ? 'june' : (world.month == 'june' ? 'july' : (world.month == 'july' ? 'august' : (world.month == 'august' ? 'september' : (world.month == 'september' ? 'october' : (world.month == 'october' ? 'november' : (world.month == 'november' ? 'december' : (world.month == 'december' ? 'january' : (world.month == 'january' ? 'february' : (world.month == 'february' ? 'march' : world.month)))))))))))) : world.month }}"` · increment `rng_counter += 1` |
| 4 | [`continue`](#intent-continue) | `int(world.miles_traveled + world.rng_counter) % 100 >= 75 && int(world.miles_traveled + world.rng_counter) % 100 < 85` | `../event_breakdown` | set `rng_last = "{{ int(world.miles_traveled + world.rng_counter) % 100 }}"` · set `day = "{{ world.day + 1 > 30 ? 1 : world.day + 1 }}"`, `event_kind = "breakdown"`, `food_lbs = "{{ world.food_lbs - (world.rations == 'bare_bones' ? 6 : (world.rations == 'meager' ? 8 : 10)) }}"`, `miles_traveled = "{{ world.miles_traveled + int((world.pace == 'grueling' ? 22 : (world.pace == 'strenuous' ? 18 : 14)) * (world.month == 'december' || world.month == 'january' || world.month == 'february' ? 70 : (world.month == 'november' ? 80 : (world.month == 'march' || world.month == 'october' ? 85 : (world.month == 'april' || world.month == 'september' ? 95 : 100)))) / 100) }}"`, `year = "{{ world.day + 1 > 30 && world.month == 'december' ? world.year + 1 : world.year }}"` · set `month = "{{ world.day + 1 > 30 ? (world.month == 'march' ? 'april' : (world.month == 'april' ? 'may' : (world.month == 'may' ? 'june' : (world.month == 'june' ? 'july' : (world.month == 'july' ? 'august' : (world.month == 'august' ? 'september' : (world.month == 'september' ? 'october' : (world.month == 'october' ? 'november' : (world.month == 'november' ? 'december' : (world.month == 'december' ? 'january' : (world.month == 'january' ? 'february' : (world.month == 'february' ? 'march' : world.month)))))))))))) : world.month }}"` · increment `rng_counter += 1` |
| 5 | [`continue`](#intent-continue) | `int(world.miles_traveled + world.rng_counter) % 100 >= 85 && int(world.miles_traveled + world.rng_counter) % 100 < 92` | `../event_weather` | set `rng_last = "{{ int(world.miles_traveled + world.rng_counter) % 100 }}"` · set `day = "{{ world.day + 1 > 30 ? 1 : world.day + 1 }}"`, `event_kind = "weather"`, `food_lbs = "{{ world.food_lbs - (world.rations == 'bare_bones' ? 6 : (world.rations == 'meager' ? 8 : 10)) }}"`, `miles_traveled = "{{ world.miles_traveled + int((world.pace == 'grueling' ? 22 : (world.pace == 'strenuous' ? 18 : 14)) * (world.month == 'december' || world.month == 'january' || world.month == 'february' ? 70 : (world.month == 'november' ? 80 : (world.month == 'march' || world.month == 'october' ? 85 : (world.month == 'april' || world.month == 'september' ? 95 : 100)))) / 100) }}"`, `year = "{{ world.day + 1 > 30 && world.month == 'december' ? world.year + 1 : world.year }}"` · set `month = "{{ world.day + 1 > 30 ? (world.month == 'march' ? 'april' : (world.month == 'april' ? 'may' : (world.month == 'may' ? 'june' : (world.month == 'june' ? 'july' : (world.month == 'july' ? 'august' : (world.month == 'august' ? 'september' : (world.month == 'september' ? 'october' : (world.month == 'october' ? 'november' : (world.month == 'november' ? 'december' : (world.month == 'december' ? 'january' : (world.month == 'january' ? 'february' : (world.month == 'february' ? 'march' : world.month)))))))))))) : world.month }}"` · increment `rng_counter += 1` |
| 6 | [`continue`](#intent-continue) | `int(world.miles_traveled + world.rng_counter) % 100 >= 92 && int(world.miles_traveled + world.rng_counter) % 100 < 97` | `../event_encounter` | set `rng_last = "{{ int(world.miles_traveled + world.rng_counter) % 100 }}"` · set `day = "{{ world.day + 1 > 30 ? 1 : world.day + 1 }}"`, `event_kind = "encounter"`, `food_lbs = "{{ world.food_lbs - (world.rations == 'bare_bones' ? 6 : (world.rations == 'meager' ? 8 : 10)) }}"`, `miles_traveled = "{{ world.miles_traveled + int((world.pace == 'grueling' ? 22 : (world.pace == 'strenuous' ? 18 : 14)) * (world.month == 'december' || world.month == 'january' || world.month == 'february' ? 70 : (world.month == 'november' ? 80 : (world.month == 'march' || world.month == 'october' ? 85 : (world.month == 'april' || world.month == 'september' ? 95 : 100)))) / 100) }}"`, `year = "{{ world.day + 1 > 30 && world.month == 'december' ? world.year + 1 : world.year }}"` · set `month = "{{ world.day + 1 > 30 ? (world.month == 'march' ? 'april' : (world.month == 'april' ? 'may' : (world.month == 'may' ? 'june' : (world.month == 'june' ? 'july' : (world.month == 'july' ? 'august' : (world.month == 'august' ? 'september' : (world.month == 'september' ? 'october' : (world.month == 'october' ? 'november' : (world.month == 'november' ? 'december' : (world.month == 'december' ? 'january' : (world.month == 'january' ? 'february' : (world.month == 'february' ? 'march' : world.month)))))))))))) : world.month }}"` · increment `rng_counter += 1` |
| 7 | [`continue`](#intent-continue) | `int(world.miles_traveled + world.rng_counter) % 100 >= 97` | `../event_supply_loss` | set `rng_last = "{{ int(world.miles_traveled + world.rng_counter) % 100 }}"` · set `day = "{{ world.day + 1 > 30 ? 1 : world.day + 1 }}"`, `event_kind = "supply_loss"`, `food_lbs = "{{ world.food_lbs - (world.rations == 'bare_bones' ? 6 : (world.rations == 'meager' ? 8 : 10)) }}"`, `miles_traveled = "{{ world.miles_traveled + int((world.pace == 'grueling' ? 22 : (world.pace == 'strenuous' ? 18 : 14)) * (world.month == 'december' || world.month == 'january' || world.month == 'february' ? 70 : (world.month == 'november' ? 80 : (world.month == 'march' || world.month == 'october' ? 85 : (world.month == 'april' || world.month == 'september' ? 95 : 100)))) / 100) }}"`, `year = "{{ world.day + 1 > 30 && world.month == 'december' ? world.year + 1 : world.year }}"` · set `month = "{{ world.day + 1 > 30 ? (world.month == 'march' ? 'april' : (world.month == 'april' ? 'may' : (world.month == 'may' ? 'june' : (world.month == 'june' ? 'july' : (world.month == 'july' ? 'august' : (world.month == 'august' ? 'september' : (world.month == 'september' ? 'october' : (world.month == 'october' ? 'november' : (world.month == 'november' ? 'december' : (world.month == 'december' ? 'january' : (world.month == 'january' ? 'february' : (world.month == 'february' ? 'march' : world.month)))))))))))) : world.month }}"` · increment `rng_counter += 1` |
| 8 | [`continue`](#intent-continue) | _default_ | `.` | set `rng_last = "{{ int(world.miles_traveled + world.rng_counter) % 100 }}"` · set `day = "{{ world.day + 1 > 30 ? 1 : world.day + 1 }}"`, `event_kind = ""`, `food_lbs = "{{ world.food_lbs - (world.rations == 'bare_bones' ? 6 : (world.rations == 'meager' ? 8 : 10)) }}"`, `miles_traveled = "{{ world.miles_traveled + int((world.pace == 'grueling' ? 22 : (world.pace == 'strenuous' ? 18 : 14)) * (world.month == 'december' || world.month == 'january' || world.month == 'february' ? 70 : (world.month == 'november' ? 80 : (world.month == 'march' || world.month == 'october' ? 85 : (world.month == 'april' || world.month == 'september' ? 95 : 100)))) / 100) }}"`, `year = "{{ world.day + 1 > 30 && world.month == 'december' ? world.year + 1 : world.year }}"` · set `month = "{{ world.day + 1 > 30 ? (world.month == 'march' ? 'april' : (world.month == 'april' ? 'may' : (world.month == 'may' ? 'june' : (world.month == 'june' ? 'july' : (world.month == 'july' ? 'august' : (world.month == 'august' ? 'september' : (world.month == 'september' ? 'october' : (world.month == 'october' ? 'november' : (world.month == 'november' ? 'december' : (world.month == 'december' ? 'january' : (world.month == 'january' ? 'february' : (world.month == 'february' ? 'march' : world.month)))))))))))) : world.month }}"` · increment `rng_counter += 1` |
| 9 | [`look`](#intent-look) |  | `.` |  |
| 10 | [`quit`](#intent-quit) |  | [`ended_lost`](#room-ended-lost) | say "The party abandons the journey between Fort Laramie and South Pass." |
| 11 | [`set_pace`](#intent-set-pace) |  | `.` | set `pace = "{{ slots.pace }}"` · say "Pace set to {{ slots.pace }}." |
| 12 | [`set_rations`](#intent-set-rations) |  | `.` | set `rations = "{{ slots.rations }}"` · say "Rations set to {{ slots.rations }}." |

### <a id="room-leg-f-awaiting-reply"></a> `leg_f_awaiting_reply`

Arrived at Snake River Crossing (prairie).

**Shows world**: `day`, `miles_traveled`, `food_lbs`, `oxen`, `party_alive`, `health_avg`, `illness_kind`, `illness_severity`, `illness_member`, `last_landmark_prose`

**On enter**:

1. set `last_landmark_prose = ""`
2. invoke `host.transport.post` with `body = "Day {{ world.day }}, {{ world.month }} {{ world.year }}. We rolled into **Snake River Crossing** (prairie) at last.\n\n- Food: {{ world.food_lbs }} lbs\n- Oxen: {{ world.oxen }}\n- Party: {{ world.party_alive }} alive\n- Health: {{ world.health_avg }}\n"`, `phase_id = "leg_f_arrival"`, `thread = "{{ run.id }}"`, `title = "Day {{ world.day }}: Snake River Crossing"`, `transport = "tui"`
3. invoke `host.agent.ask` with `agent = "trail_narrator"`, `args = map[day:{{ world.day }} food_lbs:{{ world.food_lbs }} landmark:Snake River Crossing miles_traveled:{{ world.miles_traveled }} month:{{ world.month }} party_alive:{{ world.party_alive }} year:{{ world.year }}]`, `prompt_path = "prompts/landmark_arrival.md"`, bind `last_landmark_prose ← stdout`

**Transitions**:

| # | Intent | Guard | → | Effects |
|---|---|---|---|---|
| 1 | [`approach_river`](#intent-approach-river) | `true` | [`river_crossing`](#room-river-crossing) | set `current_landmark = "Snake River Crossing"`, `river_depth_ft = "{{ int(7 * (world.month == 'april' ? 160 : (world.month == 'march' ? 140 : (world.month == 'may' ? 130 : (world.month == 'june' ? 100 : (world.month == 'july' ? 80 : (world.month == 'august' ? 70 : (world.month == 'september' ? 80 : 100))))))) / 100) }}"`, `river_width_ft = "{{ int(1000) }}"` |
| 2 | [`approach_river`](#intent-approach-river) | _default_ | `.` | _hint: No river at this landmark._ |
| 3 | [`consult_guide`](#intent-consult-guide) |  | [`trail_guide`](#room-trail-guide) | set `last_job_originating_state = "leg_f_awaiting_reply"` · _(no-history)_ |
| 4 | [`continue`](#intent-continue) | `'Snake River Crossing' == 'South Pass' && (world.month == 'october' \|\| world.month == 'november' \|\| world.month == 'december' \|\| world.month == 'january' \|\| world.month == 'february')` | [`snow_blocked`](#room-snow-blocked) | set `current_landmark = "Snake River Crossing"` · say "South Pass is snowed in. The wagons cannot get through." |
| 5 | [`continue`](#intent-continue) | _default_ | [`leg_g_executing`](#room-leg-g-executing) | set `breakdown_part = ""`, `current_event_attempts = 0`, `current_landmark = "Snake River Crossing"`, `encounter_kind = ""`, `event_kind = ""`, `illness_kind = ""`, `miles_traveled = 0`, `weather_kind = ""` |
| 6 | [`enter_fort`](#intent-enter-fort) | `false` | [`fort`](#room-fort) | set `current_landmark = "Snake River Crossing"` |
| 7 | [`enter_fort`](#intent-enter-fort) | _default_ | `.` | _hint: No fort at this landmark._ |
| 8 | [`face_robbery`](#intent-face-robbery) |  | [`frontier`](#room-frontier) |  |
| 9 | [`give_up_leg`](#intent-give-up-leg) | `world.cycle__leg_f__on_failure < 2` | [`leg_e_executing`](#room-leg-e-executing) | increment `cycle__leg_f__on_failure += 1` · set `breakdown_part = ""`, `current_event_attempts = 0`, `encounter_kind = ""`, `event_kind = ""`, `illness_kind = ""`, `miles_traveled = 0`, `weather_kind = ""` · say "The party turns back toward South Pass." |
| 10 | [`give_up_leg`](#intent-give-up-leg) | _default_ | [`leg_f_error`](#room-leg-f-error) | say "The party has given up too many times — stranded." |
| 11 | [`hunt`](#intent-hunt) |  | [`hunt`](#room-hunt) |  |
| 12 | [`quit`](#intent-quit) |  | [`ended_lost`](#room-ended-lost) | say "The party abandons the journey at Snake River Crossing." |
| 13 | [`rest`](#intent-rest) |  | [`rest_room`](#room-rest-room) |  |
| 14 | [`restart_from`](#intent-restart-from) | `slots.stage == 'independence' \|\| slots.stage == 'kansas'` | [`leg_a_executing`](#room-leg-a-executing) | set `breakdown_part = ""`, `current_event_attempts = 0`, `encounter_kind = ""`, `event_kind = ""`, `illness_kind = ""`, `miles_traveled = 0`, `weather_kind = ""` · say "The party doubles back to Independence to retry the run toward Kansas River." |
| 15 | [`restart_from`](#intent-restart-from) | `slots.stage == 'kearney'` | [`leg_b_executing`](#room-leg-b-executing) | set `breakdown_part = ""`, `current_event_attempts = 0`, `encounter_kind = ""`, `event_kind = ""`, `illness_kind = ""`, `miles_traveled = 0`, `weather_kind = ""` · say "The party doubles back to Kansas River to retry the stretch toward Fort Kearney." |
| 16 | [`restart_from`](#intent-restart-from) | `slots.stage == 'chimney'` | [`leg_c_executing`](#room-leg-c-executing) | set `breakdown_part = ""`, `current_event_attempts = 0`, `encounter_kind = ""`, `event_kind = ""`, `illness_kind = ""`, `miles_traveled = 0`, `weather_kind = ""` · say "The party doubles back to Fort Kearney to retry the stretch toward Chimney Rock." |
| 17 | [`restart_from`](#intent-restart-from) | `slots.stage == 'laramie'` | [`leg_d_executing`](#room-leg-d-executing) | set `breakdown_part = ""`, `current_event_attempts = 0`, `encounter_kind = ""`, `event_kind = ""`, `illness_kind = ""`, `miles_traveled = 0`, `weather_kind = ""` · say "The party doubles back to Chimney Rock to retry the stretch toward Fort Laramie." |
| 18 | [`restart_from`](#intent-restart-from) | `slots.stage == 'south_pass'` | [`leg_e_executing`](#room-leg-e-executing) | set `breakdown_part = ""`, `current_event_attempts = 0`, `encounter_kind = ""`, `event_kind = ""`, `illness_kind = ""`, `miles_traveled = 0`, `weather_kind = ""` · say "The party doubles back to Fort Laramie to retry the stretch toward South Pass." |
| 19 | [`restart_from`](#intent-restart-from) | `slots.stage == 'snake'` | [`leg_f_executing`](#room-leg-f-executing) | set `breakdown_part = ""`, `current_event_attempts = 0`, `encounter_kind = ""`, `event_kind = ""`, `illness_kind = ""`, `miles_traveled = 0`, `weather_kind = ""` · say "The party doubles back to South Pass to retry the stretch toward Snake River." |
| 20 | [`restart_from`](#intent-restart-from) | _default_ | `.` | _hint: Unknown restart stage._ |
| 21 | [`scout`](#intent-scout) |  | [`frontier`](#room-frontier) |  |

**Timeout**: after `10d` → `leg_g_executing`

### <a id="room-leg-f-error"></a> `leg_f_error`

Stranded between South Pass and Snake River Crossing.

**Shows world**: `day`, `party_alive`, `current_landmark`

**Transitions**:

| # | Intent | Guard | → | Effects |
|---|---|---|---|---|
| 1 | [`quit`](#intent-quit) |  | [`ended_lost`](#room-ended-lost) | say "The party gives up between South Pass and Snake River Crossing." |
| 2 | [`retry`](#intent-retry) |  | [`leg_f_executing`](#room-leg-f-executing) | set `current_event_attempts = 0`, `event_kind = ""` |

### <a id="room-leg-f-executing"></a> `leg_f_executing`  _(compound)_

On the trail from South Pass to Snake River Crossing (prairie).

**Initial child**: `traveling`

**Shows world**: `day`, `month`, `miles_traveled`, `food_lbs`, `oxen`, `party_alive`, `health_avg`, `pace`, `rations`, `current_landmark`, `event_kind`, `illness_kind`, `illness_severity`, `illness_member`, `breakdown_part`, `weather_kind`, `encounter_kind`, `rng_last`, `rng_counter`

**On enter**:

1. set `current_landmark = "South Pass"`

**Transitions**:

| # | Intent | Guard | → | Effects |
|---|---|---|---|---|
| 1 | [`on_failure`](#intent-on-failure) | `world.cycle__leg_f__on_failure < 2` | [`leg_e_executing`](#room-leg-e-executing) | increment `cycle__leg_f__on_failure += 1` |
| 2 | [`on_failure`](#intent-on-failure) | _default_ | [`leg_f_error`](#room-leg-f-error) | _hint: cycle budget exceeded for on_failure_ |

### <a id="room-leg-f-executing-event-breakdown"></a> `leg_f_executing.event_breakdown`

Wagon breakdown — {{ world.breakdown_part }}.

**Shows world**: `breakdown_part`, `spare_wheels`, `spare_axles`, `spare_tongues`, `day`, `current_event_attempts`, `last_event_prose`

**On enter**:

1. set `breakdown_part = "{{ if world.rng_last % 3 == 0 }}wheel{{ else }}{{ if world.rng_last % 3 == 1 }}axle{{ else }}tongue{{ end }}{{ end }}"`, `current_event_attempts = 0`, `last_event_prose = ""`
2. invoke `host.agent.ask` with `agent = "trail_narrator"`, `args = map[current_landmark:{{ world.current_landmark }} day:{{ world.day }} part:{{ if world.rng_last % 3 == 0 }}wheel{{ else }}{{ if world.rng_last % 3 == 1 }}axle{{ else }}tongue{{ end }}{{ end }} spares_remaining:{{ world.rng_last % 3 == 0 ? world.spare_wheels : (world.rng_last % 3 == 1 ? world.spare_axles : world.spare_tongues) }}]`, `prompt_path = "prompts/event_breakdown.md"`, bind `last_event_prose ← stdout`

**Transitions**:

| # | Intent | Guard | → | Effects |
|---|---|---|---|---|
| 1 | [`look`](#intent-look) |  | `.` |  |
| 2 | [`quit`](#intent-quit) |  | [`ended_lost`](#room-ended-lost) | say "The party abandons the broken wagon." |
| 3 | [`repair`](#intent-repair) | `world.breakdown_part == 'wheel' && world.spare_wheels >= 1` | `../traveling` | invoke `host.transport.post` with `body = "Day {{ world.day }} — fitted the spare wheel and got the wagon rolling again. One less in reserve."`, `phase_id = "leg_f_event_breakdown_wheel_repaired_{{ world.day }}"`, `thread = "{{ run.id }}"`, `title = "Wheel repaired"`, `transport = "tui"` · set `breakdown_part = ""`, `current_event_attempts = 0`, `event_kind = ""`, `spare_wheels = "{{ world.spare_wheels - 1 }}"` · say "Spare wheel installed. Back on the trail." |
| 4 | [`repair`](#intent-repair) | `world.breakdown_part == 'axle' && world.spare_axles >= 1` | `../traveling` | invoke `host.transport.post` with `body = "Day {{ world.day }} — swapped in the spare axle. Took the morning, but the wagon's true again."`, `phase_id = "leg_f_event_breakdown_axle_repaired_{{ world.day }}"`, `thread = "{{ run.id }}"`, `title = "Axle repaired"`, `transport = "tui"` · set `breakdown_part = ""`, `current_event_attempts = 0`, `event_kind = ""`, `spare_axles = "{{ world.spare_axles - 1 }}"` · say "Spare axle installed. Back on the trail." |
| 5 | [`repair`](#intent-repair) | `world.breakdown_part == 'tongue' && world.spare_tongues >= 1` | `../traveling` | invoke `host.transport.post` with `body = "Day {{ world.day }} — pinned in the spare tongue and we hitched the oxen back up."`, `phase_id = "leg_f_event_breakdown_tongue_repaired_{{ world.day }}"`, `thread = "{{ run.id }}"`, `title = "Tongue repaired"`, `transport = "tui"` · set `breakdown_part = ""`, `current_event_attempts = 0`, `event_kind = ""`, `spare_tongues = "{{ world.spare_tongues - 1 }}"` · say "Spare tongue installed. Back on the trail." |
| 6 | [`repair`](#intent-repair) | `world.current_event_attempts < 2` | `.` | _hint: Need a spare {{ world.breakdown_part }} to repair._ · increment `current_event_attempts += 1` · say "No spare {{ world.breakdown_part }} on hand. Try repair again, wait_out, or look." |
| 7 | [`repair`](#intent-repair) | _default_ | `../traveling` | invoke `host.transport.post` with `body = "Day {{ world.day }} — couldn't repair the {{ world.breakdown_part }}. Lashed it together and pressed on at a cost: a member of the party was left behind."`, `phase_id = "leg_f_event_breakdown_failed_{{ world.day }}"`, `thread = "{{ run.id }}"`, `title = "Wagon limping on"`, `transport = "tui"` · set `breakdown_part = ""`, `current_event_attempts = 0`, `event_kind = ""`, `health_avg = "{{ world.health_avg - 20 }}"`, `party_alive = "{{ world.party_alive - 1 }}"` · say "Repeated repair attempts failed; the wagon is patched poorly and the party limps on. A member is left behind." |
| 8 | [`wait_out`](#intent-wait-out) |  | `../traveling` | invoke `host.transport.post` with `body = "Day {{ world.day }} — made camp and improvised a {{ world.breakdown_part }} repair over five days. Slow going, but back on the trail."`, `phase_id = "leg_f_event_breakdown_waited_{{ world.day }}"`, `thread = "{{ run.id }}"`, `title = "Improvised repair"`, `transport = "tui"` · set `breakdown_part = ""`, `current_event_attempts = 0`, `day = "{{ world.day + 5 }}"`, `event_kind = ""` · say "The party makes camp and improvises a repair over five days." |

### <a id="room-leg-f-executing-event-disease"></a> `leg_f_executing.event_disease`

Illness has struck the party ({{ world.illness_kind }}).

**Shows world**: `illness_kind`, `illness_severity`, `illness_treatment`, `illness_member`, `health_avg`, `party_alive`, `food_lbs`, `clothing_sets`, `day`, `current_event_attempts`

**On enter**:

1. set `current_event_attempts = 0`, `health_avg = "{{ world.health_avg - 10 }}"`, `illness_member = "{{ split(world.party_names, ',')[world.rng_last % world.party_alive] }}"`
2. set `illness_kind = "{{ if world.rng_last % 5 == 0 }}dysentery{{ else }}{{ if world.rng_last % 5 == 1 }}cholera{{ else }}{{ if world.rng_last % 5 == 2 }}typhoid{{ else }}{{ if world.rng_last % 5 == 3 }}measles{{ else }}exhaustion{{ end }}{{ end }}{{ end }}{{ end }}"`, `illness_severity = "{{ world.rng_last % 5 + 1 }}"`, `illness_treatment = "rest"`
3. invoke `host.agent.decide` with `agent = "frontier_doctor"`, `args = map[clothing_sets:{{ world.clothing_sets }} current_landmark:{{ world.current_landmark }} food_lbs:{{ world.food_lbs }} health_avg:{{ world.health_avg }} party_alive:{{ world.party_alive }} rng_last:{{ world.rng_last }}]`, `prompt = "prompts/event_disease.md"`, `schema = "mcp/illness.json"`, bind `illness_kind ← submitted.illness`, `illness_severity ← submitted.severity`, `illness_treatment ← submitted.treatment`, on_error → `leg_f_error`

**Transitions**:

| # | Intent | Guard | → | Effects |
|---|---|---|---|---|
| 1 | [`look`](#intent-look) |  | `.` |  |
| 2 | [`move_on`](#intent-move-on) |  | `../traveling` | invoke `host.transport.post` with `body = "Day {{ world.day }} — pushed on through the {{ world.illness_kind }} rather than stop. Health is worse for it, but the wagon rolls."`, `phase_id = "leg_f_event_disease_moved_on_{{ world.day }}"`, `thread = "{{ run.id }}"`, `title = "Pressing on through illness"`, `transport = "tui"` · set `current_event_attempts = 0`, `event_kind = ""`, `health_avg = "{{ world.health_avg - 15 }}"`, `illness_kind = ""`, `illness_member = ""`, `illness_severity = 0`, `illness_treatment = ""` · say "The party presses on. Health worsens." |
| 3 | [`quit`](#intent-quit) |  | [`ended_lost`](#room-ended-lost) | say "The party gives up on the trail, broken by {{ world.illness_kind }}." |
| 4 | [`treat`](#intent-treat) | `world.clothing_sets >= 1 && world.food_lbs >= 50 && world.current_event_attempts < 2` | `../traveling` | invoke `host.transport.post` with `body = "Day {{ world.day }} — the {{ world.illness_kind }} has passed. Rested up, one clothing set used and 50 lbs of food spent on broth and care. Spirits steady, on the move."`, `phase_id = "leg_f_event_disease_treated_{{ world.day }}"`, `thread = "{{ run.id }}"`, `title = "Disease treated"`, `transport = "tui"` · set `clothing_sets = "{{ world.clothing_sets - 1 }}"`, `current_event_attempts = 0`, `event_kind = ""`, `food_lbs = "{{ world.food_lbs - 50 }}"`, `health_avg = "{{ world.health_avg + 20 }}"`, `illness_kind = ""`, `illness_member = ""`, `illness_severity = 0`, `illness_treatment = ""` · say "The party rests and treats the illness." |
| 5 | [`treat`](#intent-treat) | `(world.clothing_sets < 1 \|\| world.food_lbs < 50) && world.current_event_attempts < 2` | `.` | increment `current_event_attempts += 1` · say "Not enough supplies to treat the {{ world.illness_kind }} (need 1 clothing set + 50 lbs food). Try again or wait_out." |
| 6 | [`treat`](#intent-treat) | _default_ | `../traveling` | invoke `host.transport.post` with `body = "Day {{ world.day }} — after repeated attempts, the {{ world.illness_kind }} took one of the party. May the trail remember them."`, `phase_id = "leg_f_event_disease_lost_{{ world.day }}"`, `thread = "{{ run.id }}"`, `title = "Lost to illness"`, `transport = "tui"` · set `current_event_attempts = 0`, `event_kind = ""`, `illness_kind = ""`, `illness_member = ""`, `illness_severity = 0`, `illness_treatment = ""`, `party_alive = "{{ world.party_alive - 1 }}"` · say "After repeated futile attempts, a party member has died of illness." |
| 7 | [`wait_out`](#intent-wait-out) | `world.health_avg < 30` | `../traveling` | invoke `host.transport.post` with `body = "Day {{ world.day }} — the patient was too weak. {{ world.illness_kind }} claimed one of the party while we waited."`, `phase_id = "leg_f_event_disease_wait_lost_{{ world.day }}"`, `thread = "{{ run.id }}"`, `title = "Lost to illness"`, `transport = "tui"` · set `current_event_attempts = 0`, `day = "{{ world.day + 1 }}"`, `event_kind = ""`, `illness_kind = ""`, `illness_member = ""`, `illness_severity = 0`, `illness_treatment = ""`, `party_alive = "{{ world.party_alive - 1 }}"` · say "The patient was too weak. A party member dies of illness." |
| 8 | [`wait_out`](#intent-wait-out) | _default_ | `../traveling` | invoke `host.transport.post` with `body = "Day {{ world.day }} — one day of rest and the {{ world.illness_kind }} let go. We move on tomorrow."`, `phase_id = "leg_f_event_disease_wait_ok_{{ world.day }}"`, `thread = "{{ run.id }}"`, `title = "Illness passed"`, `transport = "tui"` · set `current_event_attempts = 0`, `day = "{{ world.day + 1 }}"`, `event_kind = ""`, `illness_kind = ""`, `illness_member = ""`, `illness_severity = 0`, `illness_treatment = ""` · say "The party rests a day. The illness passes." |

### <a id="room-leg-f-executing-event-encounter"></a> `leg_f_executing.event_encounter`

Encounter on the trail: {{ world.encounter_kind }}.

**Shows world**: `encounter_kind`, `food_lbs`, `clothing_sets`, `last_event_prose`

**On enter**:

1. set `current_event_attempts = 0`, `encounter_kind = "{{ if world.rng_last % 3 == 0 }}trader{{ else }}{{ if world.rng_last % 3 == 1 }}hunter{{ else }}band{{ end }}{{ end }}"`, `last_event_prose = ""`
2. invoke `host.agent.ask` with `agent = "trail_narrator"`, `args = map[clothing_sets:{{ world.clothing_sets }} current_landmark:{{ world.current_landmark }} day:{{ world.day }} food_lbs:{{ world.food_lbs }} kind:{{ if world.rng_last % 3 == 0 }}trader{{ else }}{{ if world.rng_last % 3 == 1 }}hunter{{ else }}band{{ end }}{{ end }}]`, `prompt_path = "prompts/event_encounter.md"`, bind `last_event_prose ← stdout`

**Transitions**:

| # | Intent | Guard | → | Effects |
|---|---|---|---|---|
| 1 | [`accept_trade`](#intent-accept-trade) | `world.food_lbs >= 50` | `../traveling` | invoke `host.transport.post` with `body = "Day {{ world.day }} — traded 50 lbs of food to a {{ world.encounter_kind }} for one clothing set. A fair deal on the trail."`, `phase_id = "leg_f_event_encounter_traded_{{ world.day }}"`, `thread = "{{ run.id }}"`, `title = "Trade with a {{ world.encounter_kind }}"`, `transport = "tui"` · set `clothing_sets = "{{ world.clothing_sets + 1 }}"`, `current_event_attempts = 0`, `encounter_kind = ""`, `event_kind = ""`, `food_lbs = "{{ world.food_lbs - 50 }}"` · say "Trade complete: -50 lbs food, +1 clothing set." |
| 2 | [`accept_trade`](#intent-accept-trade) | _default_ | `.` | _hint: Not enough food to trade._ |
| 3 | [`decline_trade`](#intent-decline-trade) |  | `../traveling` | invoke `host.transport.post` with `body = "Day {{ world.day }} — passed on a trade with a {{ world.encounter_kind }}. The wagon kept rolling."`, `phase_id = "leg_f_event_encounter_declined_{{ world.day }}"`, `thread = "{{ run.id }}"`, `title = "Declined a trade"`, `transport = "tui"` · set `current_event_attempts = 0`, `encounter_kind = ""`, `event_kind = ""` · say "The party declines the trade and presses on." |
| 4 | [`look`](#intent-look) |  | `.` |  |
| 5 | [`move_on`](#intent-move-on) |  | `../traveling` | invoke `host.transport.post` with `body = "Day {{ world.day }} — moved on past the {{ world.encounter_kind }}. No words exchanged."`, `phase_id = "leg_f_event_encounter_moved_on_{{ world.day }}"`, `thread = "{{ run.id }}"`, `title = "Moved on from a {{ world.encounter_kind }}"`, `transport = "tui"` · set `current_event_attempts = 0`, `encounter_kind = ""`, `event_kind = ""` · say "The party moves on past the encounter." |
| 6 | [`quit`](#intent-quit) |  | [`ended_lost`](#room-ended-lost) | say "The party gives up at the encounter." |

### <a id="room-leg-f-executing-event-supply-loss"></a> `leg_f_executing.event_supply_loss`

Supplies lost on the trail.

**Shows world**: `food_lbs`, `oxen`, `rng_last`, `last_event_prose`

**On enter**:

1. set `current_event_attempts = 0`, `last_event = "{{ if world.rng_last % 2 == 0 }}food_loss{{ else }}ox_loss{{ end }}"`, `last_event_prose = ""`
2. set `food_lbs = "{{ world.rng_last % 2 == 0 ? world.food_lbs - (10 + 10 * (world.rng_last % 4)) : world.food_lbs }}"`, `oxen = "{{ world.rng_last % 2 == 0 ? world.oxen : world.oxen - 1 }}"`
3. invoke `host.agent.ask` with `agent = "trail_narrator"`, `args = map[current_landmark:{{ world.current_landmark }} day:{{ world.day }} food_lbs:{{ world.food_lbs }} oxen:{{ world.oxen }} what:{{ world.rng_last % 2 == 0 ? 'food spoiled' : 'ox lame' }}]`, `prompt_path = "prompts/event_supply_loss.md"`, bind `last_event_prose ← stdout`

**Transitions**:

| # | Intent | Guard | → | Effects |
|---|---|---|---|---|
| 1 | [`look`](#intent-look) |  | `.` |  |
| 2 | [`move_on`](#intent-move-on) |  | `../traveling` | invoke `host.transport.post` with `body = "Day {{ world.day }} — {{ world.last_event == 'food_loss' ? 'food spoiled' : 'an ox went lame' }} on the trail. Recovered what we could and pressed on. Food: {{ world.food_lbs }} lbs, oxen: {{ world.oxen }}."`, `phase_id = "leg_f_event_supply_loss_recovered_{{ world.day }}"`, `thread = "{{ run.id }}"`, `title = "Supplies lost"`, `transport = "tui"` · set `current_event_attempts = 0`, `event_kind = ""`, `last_event = ""` · say "The party recovers what it can and presses on." |
| 3 | [`quit`](#intent-quit) |  | [`ended_lost`](#room-ended-lost) | say "The party gives up after losing supplies." |

### <a id="room-leg-f-executing-event-weather"></a> `leg_f_executing.event_weather`

Severe weather: {{ world.weather_kind }}.

**Shows world**: `weather_kind`, `day`, `food_lbs`, `health_avg`, `last_event_prose`

**On enter**:

1. set `current_event_attempts = 0`, `last_event_prose = ""`, `weather_kind = "{{ world.month == 'november' || world.month == 'december' || world.month == 'january' || world.month == 'february' ? 'snow' : (world.month == 'march' || world.month == 'april' || world.month == 'may' ? 'heavy_rain' : (world.month == 'june' || world.month == 'july' || world.month == 'august' ? (world.rng_last % 2 == 0 ? 'hail' : 'fog') : (world.rng_last % 2 == 0 ? 'heavy_rain' : 'fog'))) }}"`
2. invoke `host.agent.ask` with `agent = "trail_narrator"`, `args = map[current_landmark:{{ world.current_landmark }} day:{{ world.day }} kind:{{ world.month == 'november' || world.month == 'december' || world.month == 'january' || world.month == 'february' ? 'snow' : (world.month == 'march' || world.month == 'april' || world.month == 'may' ? 'heavy_rain' : (world.month == 'june' || world.month == 'july' || world.month == 'august' ? (world.rng_last % 2 == 0 ? 'hail' : 'fog') : (world.rng_last % 2 == 0 ? 'heavy_rain' : 'fog'))) }} month:{{ world.month }} terrain:prairie]`, `prompt_path = "prompts/event_weather.md"`, bind `last_event_prose ← stdout`

**Transitions**:

| # | Intent | Guard | → | Effects |
|---|---|---|---|---|
| 1 | [`look`](#intent-look) |  | `.` |  |
| 2 | [`push_on`](#intent-push-on) |  | `../traveling` | invoke `host.transport.post` with `body = "Day {{ world.day }} — pushed on through the {{ world.weather_kind }}. Cold and wet; health worsened by 10."`, `phase_id = "leg_f_event_weather_pushed_on_{{ world.day }}"`, `thread = "{{ run.id }}"`, `title = "Pushed on through the weather"`, `transport = "tui"` · set `current_event_attempts = 0`, `event_kind = ""`, `health_avg = "{{ world.health_avg - 10 }}"`, `weather_kind = ""` · say "The party pushes on through the weather." |
| 3 | [`quit`](#intent-quit) |  | [`ended_lost`](#room-ended-lost) | say "The party gives up in the {{ world.weather_kind }}." |
| 4 | [`wait_out`](#intent-wait-out) |  | `../traveling` | invoke `host.transport.post` with `body = "Day {{ world.day }} — sheltered until the {{ world.weather_kind }} let up. {{ world.weather_kind == 'heavy_rain' ? '20 lbs of food spoiled in the wet.' : 'No supplies lost — only days.' }}"`, `phase_id = "leg_f_event_weather_waited_{{ world.day }}"`, `thread = "{{ run.id }}"`, `title = "Weather passed"`, `transport = "tui"` · set `current_event_attempts = 0`, `day = "{{ world.day + (world.rng_last % 3) + 1 }}"`, `event_kind = ""`, `food_lbs = "{{ world.weather_kind == 'heavy_rain' ? world.food_lbs - 20 : world.food_lbs }}"`, `weather_kind = ""` · say "The party shelters until the weather passes." |

### <a id="room-leg-f-executing-traveling"></a> `leg_f_executing.traveling`

Travelling — South Pass → Snake River Crossing.

**Transitions**:

| # | Intent | Guard | → | Effects |
|---|---|---|---|---|
| 1 | [`continue`](#intent-continue) | `world.miles_traveled + int((world.pace == 'grueling' ? 22 : (world.pace == 'strenuous' ? 18 : 14)) * (world.month == 'december' \|\| world.month == 'january' \|\| world.month == 'february' ? 70 : (world.month == 'november' ? 80 : (world.month == 'march' \|\| world.month == 'october' ? 85 : (world.month == 'april' \|\| world.month == 'september' ? 95 : 100)))) / 100) >= 250` | [`leg_f_awaiting_reply`](#room-leg-f-awaiting-reply) | set `day = "{{ world.day + 1 > 30 ? 1 : world.day + 1 }}"`, `food_lbs = "{{ world.food_lbs - (world.rations == 'bare_bones' ? 6 : (world.rations == 'meager' ? 8 : 10)) }}"`, `miles_traveled = "{{ world.miles_traveled + int((world.pace == 'grueling' ? 22 : (world.pace == 'strenuous' ? 18 : 14)) * (world.month == 'december' || world.month == 'january' || world.month == 'february' ? 70 : (world.month == 'november' ? 80 : (world.month == 'march' || world.month == 'october' ? 85 : (world.month == 'april' || world.month == 'september' ? 95 : 100)))) / 100) }}"`, `year = "{{ world.day + 1 > 30 && world.month == 'december' ? world.year + 1 : world.year }}"` · set `month = "{{ world.day + 1 > 30 ? (world.month == 'march' ? 'april' : (world.month == 'april' ? 'may' : (world.month == 'may' ? 'june' : (world.month == 'june' ? 'july' : (world.month == 'july' ? 'august' : (world.month == 'august' ? 'september' : (world.month == 'september' ? 'october' : (world.month == 'october' ? 'november' : (world.month == 'november' ? 'december' : (world.month == 'december' ? 'january' : (world.month == 'january' ? 'february' : (world.month == 'february' ? 'march' : world.month)))))))))))) : world.month }}"` · say "Arrived at Snake River Crossing." |
| 2 | [`continue`](#intent-continue) | `world.food_lbs - (world.rations == 'bare_bones' ? 6 : (world.rations == 'meager' ? 8 : 10)) <= 0` | [`ended_lost`](#room-ended-lost) | set `day = "{{ world.day + 1 > 30 ? 1 : world.day + 1 }}"`, `food_lbs = 0`, `year = "{{ world.day + 1 > 30 && world.month == 'december' ? world.year + 1 : world.year }}"` · set `month = "{{ world.day + 1 > 30 ? (world.month == 'march' ? 'april' : (world.month == 'april' ? 'may' : (world.month == 'may' ? 'june' : (world.month == 'june' ? 'july' : (world.month == 'july' ? 'august' : (world.month == 'august' ? 'september' : (world.month == 'september' ? 'october' : (world.month == 'october' ? 'november' : (world.month == 'november' ? 'december' : (world.month == 'december' ? 'january' : (world.month == 'january' ? 'february' : (world.month == 'february' ? 'march' : world.month)))))))))))) : world.month }}"` · say "The party has run out of food between South Pass and Snake River Crossing. They starve on the trail." |
| 3 | [`continue`](#intent-continue) | `int(world.miles_traveled + world.rng_counter) % 100 >= 60 && int(world.miles_traveled + world.rng_counter) % 100 < 75` | `../event_disease` | set `rng_last = "{{ int(world.miles_traveled + world.rng_counter) % 100 }}"` · set `day = "{{ world.day + 1 > 30 ? 1 : world.day + 1 }}"`, `event_kind = "disease"`, `food_lbs = "{{ world.food_lbs - (world.rations == 'bare_bones' ? 6 : (world.rations == 'meager' ? 8 : 10)) }}"`, `miles_traveled = "{{ world.miles_traveled + int((world.pace == 'grueling' ? 22 : (world.pace == 'strenuous' ? 18 : 14)) * (world.month == 'december' || world.month == 'january' || world.month == 'february' ? 70 : (world.month == 'november' ? 80 : (world.month == 'march' || world.month == 'october' ? 85 : (world.month == 'april' || world.month == 'september' ? 95 : 100)))) / 100) }}"`, `year = "{{ world.day + 1 > 30 && world.month == 'december' ? world.year + 1 : world.year }}"` · set `month = "{{ world.day + 1 > 30 ? (world.month == 'march' ? 'april' : (world.month == 'april' ? 'may' : (world.month == 'may' ? 'june' : (world.month == 'june' ? 'july' : (world.month == 'july' ? 'august' : (world.month == 'august' ? 'september' : (world.month == 'september' ? 'october' : (world.month == 'october' ? 'november' : (world.month == 'november' ? 'december' : (world.month == 'december' ? 'january' : (world.month == 'january' ? 'february' : (world.month == 'february' ? 'march' : world.month)))))))))))) : world.month }}"` · increment `rng_counter += 1` |
| 4 | [`continue`](#intent-continue) | `int(world.miles_traveled + world.rng_counter) % 100 >= 75 && int(world.miles_traveled + world.rng_counter) % 100 < 85` | `../event_breakdown` | set `rng_last = "{{ int(world.miles_traveled + world.rng_counter) % 100 }}"` · set `day = "{{ world.day + 1 > 30 ? 1 : world.day + 1 }}"`, `event_kind = "breakdown"`, `food_lbs = "{{ world.food_lbs - (world.rations == 'bare_bones' ? 6 : (world.rations == 'meager' ? 8 : 10)) }}"`, `miles_traveled = "{{ world.miles_traveled + int((world.pace == 'grueling' ? 22 : (world.pace == 'strenuous' ? 18 : 14)) * (world.month == 'december' || world.month == 'january' || world.month == 'february' ? 70 : (world.month == 'november' ? 80 : (world.month == 'march' || world.month == 'october' ? 85 : (world.month == 'april' || world.month == 'september' ? 95 : 100)))) / 100) }}"`, `year = "{{ world.day + 1 > 30 && world.month == 'december' ? world.year + 1 : world.year }}"` · set `month = "{{ world.day + 1 > 30 ? (world.month == 'march' ? 'april' : (world.month == 'april' ? 'may' : (world.month == 'may' ? 'june' : (world.month == 'june' ? 'july' : (world.month == 'july' ? 'august' : (world.month == 'august' ? 'september' : (world.month == 'september' ? 'october' : (world.month == 'october' ? 'november' : (world.month == 'november' ? 'december' : (world.month == 'december' ? 'january' : (world.month == 'january' ? 'february' : (world.month == 'february' ? 'march' : world.month)))))))))))) : world.month }}"` · increment `rng_counter += 1` |
| 5 | [`continue`](#intent-continue) | `int(world.miles_traveled + world.rng_counter) % 100 >= 85 && int(world.miles_traveled + world.rng_counter) % 100 < 92` | `../event_weather` | set `rng_last = "{{ int(world.miles_traveled + world.rng_counter) % 100 }}"` · set `day = "{{ world.day + 1 > 30 ? 1 : world.day + 1 }}"`, `event_kind = "weather"`, `food_lbs = "{{ world.food_lbs - (world.rations == 'bare_bones' ? 6 : (world.rations == 'meager' ? 8 : 10)) }}"`, `miles_traveled = "{{ world.miles_traveled + int((world.pace == 'grueling' ? 22 : (world.pace == 'strenuous' ? 18 : 14)) * (world.month == 'december' || world.month == 'january' || world.month == 'february' ? 70 : (world.month == 'november' ? 80 : (world.month == 'march' || world.month == 'october' ? 85 : (world.month == 'april' || world.month == 'september' ? 95 : 100)))) / 100) }}"`, `year = "{{ world.day + 1 > 30 && world.month == 'december' ? world.year + 1 : world.year }}"` · set `month = "{{ world.day + 1 > 30 ? (world.month == 'march' ? 'april' : (world.month == 'april' ? 'may' : (world.month == 'may' ? 'june' : (world.month == 'june' ? 'july' : (world.month == 'july' ? 'august' : (world.month == 'august' ? 'september' : (world.month == 'september' ? 'october' : (world.month == 'october' ? 'november' : (world.month == 'november' ? 'december' : (world.month == 'december' ? 'january' : (world.month == 'january' ? 'february' : (world.month == 'february' ? 'march' : world.month)))))))))))) : world.month }}"` · increment `rng_counter += 1` |
| 6 | [`continue`](#intent-continue) | `int(world.miles_traveled + world.rng_counter) % 100 >= 92 && int(world.miles_traveled + world.rng_counter) % 100 < 97` | `../event_encounter` | set `rng_last = "{{ int(world.miles_traveled + world.rng_counter) % 100 }}"` · set `day = "{{ world.day + 1 > 30 ? 1 : world.day + 1 }}"`, `event_kind = "encounter"`, `food_lbs = "{{ world.food_lbs - (world.rations == 'bare_bones' ? 6 : (world.rations == 'meager' ? 8 : 10)) }}"`, `miles_traveled = "{{ world.miles_traveled + int((world.pace == 'grueling' ? 22 : (world.pace == 'strenuous' ? 18 : 14)) * (world.month == 'december' || world.month == 'january' || world.month == 'february' ? 70 : (world.month == 'november' ? 80 : (world.month == 'march' || world.month == 'october' ? 85 : (world.month == 'april' || world.month == 'september' ? 95 : 100)))) / 100) }}"`, `year = "{{ world.day + 1 > 30 && world.month == 'december' ? world.year + 1 : world.year }}"` · set `month = "{{ world.day + 1 > 30 ? (world.month == 'march' ? 'april' : (world.month == 'april' ? 'may' : (world.month == 'may' ? 'june' : (world.month == 'june' ? 'july' : (world.month == 'july' ? 'august' : (world.month == 'august' ? 'september' : (world.month == 'september' ? 'october' : (world.month == 'october' ? 'november' : (world.month == 'november' ? 'december' : (world.month == 'december' ? 'january' : (world.month == 'january' ? 'february' : (world.month == 'february' ? 'march' : world.month)))))))))))) : world.month }}"` · increment `rng_counter += 1` |
| 7 | [`continue`](#intent-continue) | `int(world.miles_traveled + world.rng_counter) % 100 >= 97` | `../event_supply_loss` | set `rng_last = "{{ int(world.miles_traveled + world.rng_counter) % 100 }}"` · set `day = "{{ world.day + 1 > 30 ? 1 : world.day + 1 }}"`, `event_kind = "supply_loss"`, `food_lbs = "{{ world.food_lbs - (world.rations == 'bare_bones' ? 6 : (world.rations == 'meager' ? 8 : 10)) }}"`, `miles_traveled = "{{ world.miles_traveled + int((world.pace == 'grueling' ? 22 : (world.pace == 'strenuous' ? 18 : 14)) * (world.month == 'december' || world.month == 'january' || world.month == 'february' ? 70 : (world.month == 'november' ? 80 : (world.month == 'march' || world.month == 'october' ? 85 : (world.month == 'april' || world.month == 'september' ? 95 : 100)))) / 100) }}"`, `year = "{{ world.day + 1 > 30 && world.month == 'december' ? world.year + 1 : world.year }}"` · set `month = "{{ world.day + 1 > 30 ? (world.month == 'march' ? 'april' : (world.month == 'april' ? 'may' : (world.month == 'may' ? 'june' : (world.month == 'june' ? 'july' : (world.month == 'july' ? 'august' : (world.month == 'august' ? 'september' : (world.month == 'september' ? 'october' : (world.month == 'october' ? 'november' : (world.month == 'november' ? 'december' : (world.month == 'december' ? 'january' : (world.month == 'january' ? 'february' : (world.month == 'february' ? 'march' : world.month)))))))))))) : world.month }}"` · increment `rng_counter += 1` |
| 8 | [`continue`](#intent-continue) | _default_ | `.` | set `rng_last = "{{ int(world.miles_traveled + world.rng_counter) % 100 }}"` · set `day = "{{ world.day + 1 > 30 ? 1 : world.day + 1 }}"`, `event_kind = ""`, `food_lbs = "{{ world.food_lbs - (world.rations == 'bare_bones' ? 6 : (world.rations == 'meager' ? 8 : 10)) }}"`, `miles_traveled = "{{ world.miles_traveled + int((world.pace == 'grueling' ? 22 : (world.pace == 'strenuous' ? 18 : 14)) * (world.month == 'december' || world.month == 'january' || world.month == 'february' ? 70 : (world.month == 'november' ? 80 : (world.month == 'march' || world.month == 'october' ? 85 : (world.month == 'april' || world.month == 'september' ? 95 : 100)))) / 100) }}"`, `year = "{{ world.day + 1 > 30 && world.month == 'december' ? world.year + 1 : world.year }}"` · set `month = "{{ world.day + 1 > 30 ? (world.month == 'march' ? 'april' : (world.month == 'april' ? 'may' : (world.month == 'may' ? 'june' : (world.month == 'june' ? 'july' : (world.month == 'july' ? 'august' : (world.month == 'august' ? 'september' : (world.month == 'september' ? 'october' : (world.month == 'october' ? 'november' : (world.month == 'november' ? 'december' : (world.month == 'december' ? 'january' : (world.month == 'january' ? 'february' : (world.month == 'february' ? 'march' : world.month)))))))))))) : world.month }}"` · increment `rng_counter += 1` |
| 9 | [`look`](#intent-look) |  | `.` |  |
| 10 | [`quit`](#intent-quit) |  | [`ended_lost`](#room-ended-lost) | say "The party abandons the journey between South Pass and Snake River Crossing." |
| 11 | [`set_pace`](#intent-set-pace) |  | `.` | set `pace = "{{ slots.pace }}"` · say "Pace set to {{ slots.pace }}." |
| 12 | [`set_rations`](#intent-set-rations) |  | `.` | set `rations = "{{ slots.rations }}"` · say "Rations set to {{ slots.rations }}." |

### <a id="room-leg-g-awaiting-reply"></a> `leg_g_awaiting_reply`

Arrived at Willamette Valley (mountain).

**Shows world**: `day`, `miles_traveled`, `food_lbs`, `oxen`, `party_alive`, `health_avg`, `illness_kind`, `illness_severity`, `illness_member`, `last_landmark_prose`

**On enter**:

1. set `last_landmark_prose = ""`
2. invoke `host.transport.post` with `body = "Day {{ world.day }}, {{ world.month }} {{ world.year }}. We rolled into **Willamette Valley** (mountain) at last.\n\n- Food: {{ world.food_lbs }} lbs\n- Oxen: {{ world.oxen }}\n- Party: {{ world.party_alive }} alive\n- Health: {{ world.health_avg }}\n"`, `phase_id = "leg_g_arrival"`, `thread = "{{ run.id }}"`, `title = "Day {{ world.day }}: Willamette Valley"`, `transport = "tui"`
3. invoke `host.agent.ask` with `agent = "trail_narrator"`, `args = map[day:{{ world.day }} food_lbs:{{ world.food_lbs }} landmark:Willamette Valley miles_traveled:{{ world.miles_traveled }} month:{{ world.month }} party_alive:{{ world.party_alive }} year:{{ world.year }}]`, `prompt_path = "prompts/landmark_arrival.md"`, bind `last_landmark_prose ← stdout`

**Transitions**:

| # | Intent | Guard | → | Effects |
|---|---|---|---|---|
| 1 | [`approach_river`](#intent-approach-river) | `false` | [`river_crossing`](#room-river-crossing) | set `current_landmark = "Willamette Valley"`, `river_depth_ft = "{{ int(0 * (world.month == 'april' ? 160 : (world.month == 'march' ? 140 : (world.month == 'may' ? 130 : (world.month == 'june' ? 100 : (world.month == 'july' ? 80 : (world.month == 'august' ? 70 : (world.month == 'september' ? 80 : 100))))))) / 100) }}"`, `river_width_ft = "{{ int(0) }}"` |
| 2 | [`approach_river`](#intent-approach-river) | _default_ | `.` | _hint: No river at this landmark._ |
| 3 | [`consult_guide`](#intent-consult-guide) |  | [`trail_guide`](#room-trail-guide) | set `last_job_originating_state = "leg_g_awaiting_reply"` · _(no-history)_ |
| 4 | [`continue`](#intent-continue) | `'Willamette Valley' == 'South Pass' && (world.month == 'october' \|\| world.month == 'november' \|\| world.month == 'december' \|\| world.month == 'january' \|\| world.month == 'february')` | [`snow_blocked`](#room-snow-blocked) | set `current_landmark = "Willamette Valley"` · say "South Pass is snowed in. The wagons cannot get through." |
| 5 | [`continue`](#intent-continue) | _default_ | [`ended_won`](#room-ended-won) | set `breakdown_part = ""`, `current_event_attempts = 0`, `current_landmark = "Willamette Valley"`, `encounter_kind = ""`, `event_kind = ""`, `illness_kind = ""`, `miles_traveled = 0`, `weather_kind = ""` |
| 6 | [`enter_fort`](#intent-enter-fort) | `false` | [`fort`](#room-fort) | set `current_landmark = "Willamette Valley"` |
| 7 | [`enter_fort`](#intent-enter-fort) | _default_ | `.` | _hint: No fort at this landmark._ |
| 8 | [`face_robbery`](#intent-face-robbery) |  | [`frontier`](#room-frontier) |  |
| 9 | [`give_up_leg`](#intent-give-up-leg) | `world.cycle__leg_g__on_failure < 2` | [`leg_f_executing`](#room-leg-f-executing) | increment `cycle__leg_g__on_failure += 1` · set `breakdown_part = ""`, `current_event_attempts = 0`, `encounter_kind = ""`, `event_kind = ""`, `illness_kind = ""`, `miles_traveled = 0`, `weather_kind = ""` · say "The party turns back toward Snake River Crossing." |
| 10 | [`give_up_leg`](#intent-give-up-leg) | _default_ | [`leg_g_error`](#room-leg-g-error) | say "The party has given up too many times — stranded." |
| 11 | [`hunt`](#intent-hunt) |  | [`hunt`](#room-hunt) |  |
| 12 | [`quit`](#intent-quit) |  | [`ended_lost`](#room-ended-lost) | say "The party abandons the journey at Willamette Valley." |
| 13 | [`rest`](#intent-rest) |  | [`rest_room`](#room-rest-room) |  |
| 14 | [`restart_from`](#intent-restart-from) | `slots.stage == 'independence' \|\| slots.stage == 'kansas'` | [`leg_a_executing`](#room-leg-a-executing) | set `breakdown_part = ""`, `current_event_attempts = 0`, `encounter_kind = ""`, `event_kind = ""`, `illness_kind = ""`, `miles_traveled = 0`, `weather_kind = ""` · say "The party doubles back to Independence to retry the run toward Kansas River." |
| 15 | [`restart_from`](#intent-restart-from) | `slots.stage == 'kearney'` | [`leg_b_executing`](#room-leg-b-executing) | set `breakdown_part = ""`, `current_event_attempts = 0`, `encounter_kind = ""`, `event_kind = ""`, `illness_kind = ""`, `miles_traveled = 0`, `weather_kind = ""` · say "The party doubles back to Kansas River to retry the stretch toward Fort Kearney." |
| 16 | [`restart_from`](#intent-restart-from) | `slots.stage == 'chimney'` | [`leg_c_executing`](#room-leg-c-executing) | set `breakdown_part = ""`, `current_event_attempts = 0`, `encounter_kind = ""`, `event_kind = ""`, `illness_kind = ""`, `miles_traveled = 0`, `weather_kind = ""` · say "The party doubles back to Fort Kearney to retry the stretch toward Chimney Rock." |
| 17 | [`restart_from`](#intent-restart-from) | `slots.stage == 'laramie'` | [`leg_d_executing`](#room-leg-d-executing) | set `breakdown_part = ""`, `current_event_attempts = 0`, `encounter_kind = ""`, `event_kind = ""`, `illness_kind = ""`, `miles_traveled = 0`, `weather_kind = ""` · say "The party doubles back to Chimney Rock to retry the stretch toward Fort Laramie." |
| 18 | [`restart_from`](#intent-restart-from) | `slots.stage == 'south_pass'` | [`leg_e_executing`](#room-leg-e-executing) | set `breakdown_part = ""`, `current_event_attempts = 0`, `encounter_kind = ""`, `event_kind = ""`, `illness_kind = ""`, `miles_traveled = 0`, `weather_kind = ""` · say "The party doubles back to Fort Laramie to retry the stretch toward South Pass." |
| 19 | [`restart_from`](#intent-restart-from) | `slots.stage == 'snake'` | [`leg_f_executing`](#room-leg-f-executing) | set `breakdown_part = ""`, `current_event_attempts = 0`, `encounter_kind = ""`, `event_kind = ""`, `illness_kind = ""`, `miles_traveled = 0`, `weather_kind = ""` · say "The party doubles back to South Pass to retry the stretch toward Snake River." |
| 20 | [`restart_from`](#intent-restart-from) | _default_ | `.` | _hint: Unknown restart stage._ |
| 21 | [`scout`](#intent-scout) |  | [`frontier`](#room-frontier) |  |

**Timeout**: after `10d` → `ended_won`

### <a id="room-leg-g-error"></a> `leg_g_error`

Stranded between Snake River Crossing and Willamette Valley.

**Shows world**: `day`, `party_alive`, `current_landmark`

**Transitions**:

| # | Intent | Guard | → | Effects |
|---|---|---|---|---|
| 1 | [`quit`](#intent-quit) |  | [`ended_lost`](#room-ended-lost) | say "The party gives up between Snake River Crossing and Willamette Valley." |
| 2 | [`retry`](#intent-retry) |  | [`leg_g_executing`](#room-leg-g-executing) | set `current_event_attempts = 0`, `event_kind = ""` |

### <a id="room-leg-g-executing"></a> `leg_g_executing`  _(compound)_

On the trail from Snake River Crossing to Willamette Valley (mountain).

**Initial child**: `traveling`

**Shows world**: `day`, `month`, `miles_traveled`, `food_lbs`, `oxen`, `party_alive`, `health_avg`, `pace`, `rations`, `current_landmark`, `event_kind`, `illness_kind`, `illness_severity`, `illness_member`, `breakdown_part`, `weather_kind`, `encounter_kind`, `rng_last`, `rng_counter`

**On enter**:

1. set `current_landmark = "Snake River Crossing"`

**Transitions**:

| # | Intent | Guard | → | Effects |
|---|---|---|---|---|
| 1 | [`on_failure`](#intent-on-failure) | `world.cycle__leg_g__on_failure < 2` | [`leg_f_executing`](#room-leg-f-executing) | increment `cycle__leg_g__on_failure += 1` |
| 2 | [`on_failure`](#intent-on-failure) | _default_ | [`leg_g_error`](#room-leg-g-error) | _hint: cycle budget exceeded for on_failure_ |

### <a id="room-leg-g-executing-event-breakdown"></a> `leg_g_executing.event_breakdown`

Wagon breakdown — {{ world.breakdown_part }}.

**Shows world**: `breakdown_part`, `spare_wheels`, `spare_axles`, `spare_tongues`, `day`, `current_event_attempts`, `last_event_prose`

**On enter**:

1. set `breakdown_part = "{{ if world.rng_last % 3 == 0 }}wheel{{ else }}{{ if world.rng_last % 3 == 1 }}axle{{ else }}tongue{{ end }}{{ end }}"`, `current_event_attempts = 0`, `last_event_prose = ""`
2. invoke `host.agent.ask` with `agent = "trail_narrator"`, `args = map[current_landmark:{{ world.current_landmark }} day:{{ world.day }} part:{{ if world.rng_last % 3 == 0 }}wheel{{ else }}{{ if world.rng_last % 3 == 1 }}axle{{ else }}tongue{{ end }}{{ end }} spares_remaining:{{ world.rng_last % 3 == 0 ? world.spare_wheels : (world.rng_last % 3 == 1 ? world.spare_axles : world.spare_tongues) }}]`, `prompt_path = "prompts/event_breakdown.md"`, bind `last_event_prose ← stdout`

**Transitions**:

| # | Intent | Guard | → | Effects |
|---|---|---|---|---|
| 1 | [`look`](#intent-look) |  | `.` |  |
| 2 | [`quit`](#intent-quit) |  | [`ended_lost`](#room-ended-lost) | say "The party abandons the broken wagon." |
| 3 | [`repair`](#intent-repair) | `world.breakdown_part == 'wheel' && world.spare_wheels >= 1` | `../traveling` | invoke `host.transport.post` with `body = "Day {{ world.day }} — fitted the spare wheel and got the wagon rolling again. One less in reserve."`, `phase_id = "leg_g_event_breakdown_wheel_repaired_{{ world.day }}"`, `thread = "{{ run.id }}"`, `title = "Wheel repaired"`, `transport = "tui"` · set `breakdown_part = ""`, `current_event_attempts = 0`, `event_kind = ""`, `spare_wheels = "{{ world.spare_wheels - 1 }}"` · say "Spare wheel installed. Back on the trail." |
| 4 | [`repair`](#intent-repair) | `world.breakdown_part == 'axle' && world.spare_axles >= 1` | `../traveling` | invoke `host.transport.post` with `body = "Day {{ world.day }} — swapped in the spare axle. Took the morning, but the wagon's true again."`, `phase_id = "leg_g_event_breakdown_axle_repaired_{{ world.day }}"`, `thread = "{{ run.id }}"`, `title = "Axle repaired"`, `transport = "tui"` · set `breakdown_part = ""`, `current_event_attempts = 0`, `event_kind = ""`, `spare_axles = "{{ world.spare_axles - 1 }}"` · say "Spare axle installed. Back on the trail." |
| 5 | [`repair`](#intent-repair) | `world.breakdown_part == 'tongue' && world.spare_tongues >= 1` | `../traveling` | invoke `host.transport.post` with `body = "Day {{ world.day }} — pinned in the spare tongue and we hitched the oxen back up."`, `phase_id = "leg_g_event_breakdown_tongue_repaired_{{ world.day }}"`, `thread = "{{ run.id }}"`, `title = "Tongue repaired"`, `transport = "tui"` · set `breakdown_part = ""`, `current_event_attempts = 0`, `event_kind = ""`, `spare_tongues = "{{ world.spare_tongues - 1 }}"` · say "Spare tongue installed. Back on the trail." |
| 6 | [`repair`](#intent-repair) | `world.current_event_attempts < 2` | `.` | _hint: Need a spare {{ world.breakdown_part }} to repair._ · increment `current_event_attempts += 1` · say "No spare {{ world.breakdown_part }} on hand. Try repair again, wait_out, or look." |
| 7 | [`repair`](#intent-repair) | _default_ | `../traveling` | invoke `host.transport.post` with `body = "Day {{ world.day }} — couldn't repair the {{ world.breakdown_part }}. Lashed it together and pressed on at a cost: a member of the party was left behind."`, `phase_id = "leg_g_event_breakdown_failed_{{ world.day }}"`, `thread = "{{ run.id }}"`, `title = "Wagon limping on"`, `transport = "tui"` · set `breakdown_part = ""`, `current_event_attempts = 0`, `event_kind = ""`, `health_avg = "{{ world.health_avg - 20 }}"`, `party_alive = "{{ world.party_alive - 1 }}"` · say "Repeated repair attempts failed; the wagon is patched poorly and the party limps on. A member is left behind." |
| 8 | [`wait_out`](#intent-wait-out) |  | `../traveling` | invoke `host.transport.post` with `body = "Day {{ world.day }} — made camp and improvised a {{ world.breakdown_part }} repair over five days. Slow going, but back on the trail."`, `phase_id = "leg_g_event_breakdown_waited_{{ world.day }}"`, `thread = "{{ run.id }}"`, `title = "Improvised repair"`, `transport = "tui"` · set `breakdown_part = ""`, `current_event_attempts = 0`, `day = "{{ world.day + 5 }}"`, `event_kind = ""` · say "The party makes camp and improvises a repair over five days." |

### <a id="room-leg-g-executing-event-disease"></a> `leg_g_executing.event_disease`

Illness has struck the party ({{ world.illness_kind }}).

**Shows world**: `illness_kind`, `illness_severity`, `illness_treatment`, `illness_member`, `health_avg`, `party_alive`, `food_lbs`, `clothing_sets`, `day`, `current_event_attempts`

**On enter**:

1. set `current_event_attempts = 0`, `health_avg = "{{ world.health_avg - 10 }}"`, `illness_member = "{{ split(world.party_names, ',')[world.rng_last % world.party_alive] }}"`
2. set `illness_kind = "{{ if world.rng_last % 5 == 0 }}dysentery{{ else }}{{ if world.rng_last % 5 == 1 }}cholera{{ else }}{{ if world.rng_last % 5 == 2 }}typhoid{{ else }}{{ if world.rng_last % 5 == 3 }}measles{{ else }}exhaustion{{ end }}{{ end }}{{ end }}{{ end }}"`, `illness_severity = "{{ world.rng_last % 5 + 1 }}"`, `illness_treatment = "rest"`
3. invoke `host.agent.decide` with `agent = "frontier_doctor"`, `args = map[clothing_sets:{{ world.clothing_sets }} current_landmark:{{ world.current_landmark }} food_lbs:{{ world.food_lbs }} health_avg:{{ world.health_avg }} party_alive:{{ world.party_alive }} rng_last:{{ world.rng_last }}]`, `prompt = "prompts/event_disease.md"`, `schema = "mcp/illness.json"`, bind `illness_kind ← submitted.illness`, `illness_severity ← submitted.severity`, `illness_treatment ← submitted.treatment`, on_error → `leg_g_error`

**Transitions**:

| # | Intent | Guard | → | Effects |
|---|---|---|---|---|
| 1 | [`look`](#intent-look) |  | `.` |  |
| 2 | [`move_on`](#intent-move-on) |  | `../traveling` | invoke `host.transport.post` with `body = "Day {{ world.day }} — pushed on through the {{ world.illness_kind }} rather than stop. Health is worse for it, but the wagon rolls."`, `phase_id = "leg_g_event_disease_moved_on_{{ world.day }}"`, `thread = "{{ run.id }}"`, `title = "Pressing on through illness"`, `transport = "tui"` · set `current_event_attempts = 0`, `event_kind = ""`, `health_avg = "{{ world.health_avg - 15 }}"`, `illness_kind = ""`, `illness_member = ""`, `illness_severity = 0`, `illness_treatment = ""` · say "The party presses on. Health worsens." |
| 3 | [`quit`](#intent-quit) |  | [`ended_lost`](#room-ended-lost) | say "The party gives up on the trail, broken by {{ world.illness_kind }}." |
| 4 | [`treat`](#intent-treat) | `world.clothing_sets >= 1 && world.food_lbs >= 50 && world.current_event_attempts < 2` | `../traveling` | invoke `host.transport.post` with `body = "Day {{ world.day }} — the {{ world.illness_kind }} has passed. Rested up, one clothing set used and 50 lbs of food spent on broth and care. Spirits steady, on the move."`, `phase_id = "leg_g_event_disease_treated_{{ world.day }}"`, `thread = "{{ run.id }}"`, `title = "Disease treated"`, `transport = "tui"` · set `clothing_sets = "{{ world.clothing_sets - 1 }}"`, `current_event_attempts = 0`, `event_kind = ""`, `food_lbs = "{{ world.food_lbs - 50 }}"`, `health_avg = "{{ world.health_avg + 20 }}"`, `illness_kind = ""`, `illness_member = ""`, `illness_severity = 0`, `illness_treatment = ""` · say "The party rests and treats the illness." |
| 5 | [`treat`](#intent-treat) | `(world.clothing_sets < 1 \|\| world.food_lbs < 50) && world.current_event_attempts < 2` | `.` | increment `current_event_attempts += 1` · say "Not enough supplies to treat the {{ world.illness_kind }} (need 1 clothing set + 50 lbs food). Try again or wait_out." |
| 6 | [`treat`](#intent-treat) | _default_ | `../traveling` | invoke `host.transport.post` with `body = "Day {{ world.day }} — after repeated attempts, the {{ world.illness_kind }} took one of the party. May the trail remember them."`, `phase_id = "leg_g_event_disease_lost_{{ world.day }}"`, `thread = "{{ run.id }}"`, `title = "Lost to illness"`, `transport = "tui"` · set `current_event_attempts = 0`, `event_kind = ""`, `illness_kind = ""`, `illness_member = ""`, `illness_severity = 0`, `illness_treatment = ""`, `party_alive = "{{ world.party_alive - 1 }}"` · say "After repeated futile attempts, a party member has died of illness." |
| 7 | [`wait_out`](#intent-wait-out) | `world.health_avg < 30` | `../traveling` | invoke `host.transport.post` with `body = "Day {{ world.day }} — the patient was too weak. {{ world.illness_kind }} claimed one of the party while we waited."`, `phase_id = "leg_g_event_disease_wait_lost_{{ world.day }}"`, `thread = "{{ run.id }}"`, `title = "Lost to illness"`, `transport = "tui"` · set `current_event_attempts = 0`, `day = "{{ world.day + 1 }}"`, `event_kind = ""`, `illness_kind = ""`, `illness_member = ""`, `illness_severity = 0`, `illness_treatment = ""`, `party_alive = "{{ world.party_alive - 1 }}"` · say "The patient was too weak. A party member dies of illness." |
| 8 | [`wait_out`](#intent-wait-out) | _default_ | `../traveling` | invoke `host.transport.post` with `body = "Day {{ world.day }} — one day of rest and the {{ world.illness_kind }} let go. We move on tomorrow."`, `phase_id = "leg_g_event_disease_wait_ok_{{ world.day }}"`, `thread = "{{ run.id }}"`, `title = "Illness passed"`, `transport = "tui"` · set `current_event_attempts = 0`, `day = "{{ world.day + 1 }}"`, `event_kind = ""`, `illness_kind = ""`, `illness_member = ""`, `illness_severity = 0`, `illness_treatment = ""` · say "The party rests a day. The illness passes." |

### <a id="room-leg-g-executing-event-encounter"></a> `leg_g_executing.event_encounter`

Encounter on the trail: {{ world.encounter_kind }}.

**Shows world**: `encounter_kind`, `food_lbs`, `clothing_sets`, `last_event_prose`

**On enter**:

1. set `current_event_attempts = 0`, `encounter_kind = "{{ if world.rng_last % 3 == 0 }}trader{{ else }}{{ if world.rng_last % 3 == 1 }}hunter{{ else }}band{{ end }}{{ end }}"`, `last_event_prose = ""`
2. invoke `host.agent.ask` with `agent = "trail_narrator"`, `args = map[clothing_sets:{{ world.clothing_sets }} current_landmark:{{ world.current_landmark }} day:{{ world.day }} food_lbs:{{ world.food_lbs }} kind:{{ if world.rng_last % 3 == 0 }}trader{{ else }}{{ if world.rng_last % 3 == 1 }}hunter{{ else }}band{{ end }}{{ end }}]`, `prompt_path = "prompts/event_encounter.md"`, bind `last_event_prose ← stdout`

**Transitions**:

| # | Intent | Guard | → | Effects |
|---|---|---|---|---|
| 1 | [`accept_trade`](#intent-accept-trade) | `world.food_lbs >= 50` | `../traveling` | invoke `host.transport.post` with `body = "Day {{ world.day }} — traded 50 lbs of food to a {{ world.encounter_kind }} for one clothing set. A fair deal on the trail."`, `phase_id = "leg_g_event_encounter_traded_{{ world.day }}"`, `thread = "{{ run.id }}"`, `title = "Trade with a {{ world.encounter_kind }}"`, `transport = "tui"` · set `clothing_sets = "{{ world.clothing_sets + 1 }}"`, `current_event_attempts = 0`, `encounter_kind = ""`, `event_kind = ""`, `food_lbs = "{{ world.food_lbs - 50 }}"` · say "Trade complete: -50 lbs food, +1 clothing set." |
| 2 | [`accept_trade`](#intent-accept-trade) | _default_ | `.` | _hint: Not enough food to trade._ |
| 3 | [`decline_trade`](#intent-decline-trade) |  | `../traveling` | invoke `host.transport.post` with `body = "Day {{ world.day }} — passed on a trade with a {{ world.encounter_kind }}. The wagon kept rolling."`, `phase_id = "leg_g_event_encounter_declined_{{ world.day }}"`, `thread = "{{ run.id }}"`, `title = "Declined a trade"`, `transport = "tui"` · set `current_event_attempts = 0`, `encounter_kind = ""`, `event_kind = ""` · say "The party declines the trade and presses on." |
| 4 | [`look`](#intent-look) |  | `.` |  |
| 5 | [`move_on`](#intent-move-on) |  | `../traveling` | invoke `host.transport.post` with `body = "Day {{ world.day }} — moved on past the {{ world.encounter_kind }}. No words exchanged."`, `phase_id = "leg_g_event_encounter_moved_on_{{ world.day }}"`, `thread = "{{ run.id }}"`, `title = "Moved on from a {{ world.encounter_kind }}"`, `transport = "tui"` · set `current_event_attempts = 0`, `encounter_kind = ""`, `event_kind = ""` · say "The party moves on past the encounter." |
| 6 | [`quit`](#intent-quit) |  | [`ended_lost`](#room-ended-lost) | say "The party gives up at the encounter." |

### <a id="room-leg-g-executing-event-supply-loss"></a> `leg_g_executing.event_supply_loss`

Supplies lost on the trail.

**Shows world**: `food_lbs`, `oxen`, `rng_last`, `last_event_prose`

**On enter**:

1. set `current_event_attempts = 0`, `last_event = "{{ if world.rng_last % 2 == 0 }}food_loss{{ else }}ox_loss{{ end }}"`, `last_event_prose = ""`
2. set `food_lbs = "{{ world.rng_last % 2 == 0 ? world.food_lbs - (10 + 10 * (world.rng_last % 4)) : world.food_lbs }}"`, `oxen = "{{ world.rng_last % 2 == 0 ? world.oxen : world.oxen - 1 }}"`
3. invoke `host.agent.ask` with `agent = "trail_narrator"`, `args = map[current_landmark:{{ world.current_landmark }} day:{{ world.day }} food_lbs:{{ world.food_lbs }} oxen:{{ world.oxen }} what:{{ world.rng_last % 2 == 0 ? 'food spoiled' : 'ox lame' }}]`, `prompt_path = "prompts/event_supply_loss.md"`, bind `last_event_prose ← stdout`

**Transitions**:

| # | Intent | Guard | → | Effects |
|---|---|---|---|---|
| 1 | [`look`](#intent-look) |  | `.` |  |
| 2 | [`move_on`](#intent-move-on) |  | `../traveling` | invoke `host.transport.post` with `body = "Day {{ world.day }} — {{ world.last_event == 'food_loss' ? 'food spoiled' : 'an ox went lame' }} on the trail. Recovered what we could and pressed on. Food: {{ world.food_lbs }} lbs, oxen: {{ world.oxen }}."`, `phase_id = "leg_g_event_supply_loss_recovered_{{ world.day }}"`, `thread = "{{ run.id }}"`, `title = "Supplies lost"`, `transport = "tui"` · set `current_event_attempts = 0`, `event_kind = ""`, `last_event = ""` · say "The party recovers what it can and presses on." |
| 3 | [`quit`](#intent-quit) |  | [`ended_lost`](#room-ended-lost) | say "The party gives up after losing supplies." |

### <a id="room-leg-g-executing-event-weather"></a> `leg_g_executing.event_weather`

Severe weather: {{ world.weather_kind }}.

**Shows world**: `weather_kind`, `day`, `food_lbs`, `health_avg`, `last_event_prose`

**On enter**:

1. set `current_event_attempts = 0`, `last_event_prose = ""`, `weather_kind = "{{ world.month == 'november' || world.month == 'december' || world.month == 'january' || world.month == 'february' ? 'snow' : (world.month == 'march' || world.month == 'april' || world.month == 'may' ? 'heavy_rain' : (world.month == 'june' || world.month == 'july' || world.month == 'august' ? (world.rng_last % 2 == 0 ? 'hail' : 'fog') : (world.rng_last % 2 == 0 ? 'heavy_rain' : 'fog'))) }}"`
2. invoke `host.agent.ask` with `agent = "trail_narrator"`, `args = map[current_landmark:{{ world.current_landmark }} day:{{ world.day }} kind:{{ world.month == 'november' || world.month == 'december' || world.month == 'january' || world.month == 'february' ? 'snow' : (world.month == 'march' || world.month == 'april' || world.month == 'may' ? 'heavy_rain' : (world.month == 'june' || world.month == 'july' || world.month == 'august' ? (world.rng_last % 2 == 0 ? 'hail' : 'fog') : (world.rng_last % 2 == 0 ? 'heavy_rain' : 'fog'))) }} month:{{ world.month }} terrain:mountain]`, `prompt_path = "prompts/event_weather.md"`, bind `last_event_prose ← stdout`

**Transitions**:

| # | Intent | Guard | → | Effects |
|---|---|---|---|---|
| 1 | [`look`](#intent-look) |  | `.` |  |
| 2 | [`push_on`](#intent-push-on) |  | `../traveling` | invoke `host.transport.post` with `body = "Day {{ world.day }} — pushed on through the {{ world.weather_kind }}. Cold and wet; health worsened by 10."`, `phase_id = "leg_g_event_weather_pushed_on_{{ world.day }}"`, `thread = "{{ run.id }}"`, `title = "Pushed on through the weather"`, `transport = "tui"` · set `current_event_attempts = 0`, `event_kind = ""`, `health_avg = "{{ world.health_avg - 10 }}"`, `weather_kind = ""` · say "The party pushes on through the weather." |
| 3 | [`quit`](#intent-quit) |  | [`ended_lost`](#room-ended-lost) | say "The party gives up in the {{ world.weather_kind }}." |
| 4 | [`wait_out`](#intent-wait-out) |  | `../traveling` | invoke `host.transport.post` with `body = "Day {{ world.day }} — sheltered until the {{ world.weather_kind }} let up. {{ world.weather_kind == 'heavy_rain' ? '20 lbs of food spoiled in the wet.' : 'No supplies lost — only days.' }}"`, `phase_id = "leg_g_event_weather_waited_{{ world.day }}"`, `thread = "{{ run.id }}"`, `title = "Weather passed"`, `transport = "tui"` · set `current_event_attempts = 0`, `day = "{{ world.day + (world.rng_last % 3) + 1 }}"`, `event_kind = ""`, `food_lbs = "{{ world.weather_kind == 'heavy_rain' ? world.food_lbs - 20 : world.food_lbs }}"`, `weather_kind = ""` · say "The party shelters until the weather passes." |

### <a id="room-leg-g-executing-traveling"></a> `leg_g_executing.traveling`

Travelling — Snake River Crossing → Willamette Valley.

**Transitions**:

| # | Intent | Guard | → | Effects |
|---|---|---|---|---|
| 1 | [`continue`](#intent-continue) | `world.miles_traveled + int((world.pace == 'grueling' ? 22 : (world.pace == 'strenuous' ? 18 : 14)) * (world.month == 'december' \|\| world.month == 'january' \|\| world.month == 'february' ? 70 : (world.month == 'november' ? 80 : (world.month == 'march' \|\| world.month == 'october' ? 85 : (world.month == 'april' \|\| world.month == 'september' ? 95 : 100)))) / 100) >= 318` | [`leg_g_awaiting_reply`](#room-leg-g-awaiting-reply) | set `day = "{{ world.day + 1 > 30 ? 1 : world.day + 1 }}"`, `food_lbs = "{{ world.food_lbs - (world.rations == 'bare_bones' ? 6 : (world.rations == 'meager' ? 8 : 10)) }}"`, `miles_traveled = "{{ world.miles_traveled + int((world.pace == 'grueling' ? 22 : (world.pace == 'strenuous' ? 18 : 14)) * (world.month == 'december' || world.month == 'january' || world.month == 'february' ? 70 : (world.month == 'november' ? 80 : (world.month == 'march' || world.month == 'october' ? 85 : (world.month == 'april' || world.month == 'september' ? 95 : 100)))) / 100) }}"`, `year = "{{ world.day + 1 > 30 && world.month == 'december' ? world.year + 1 : world.year }}"` · set `month = "{{ world.day + 1 > 30 ? (world.month == 'march' ? 'april' : (world.month == 'april' ? 'may' : (world.month == 'may' ? 'june' : (world.month == 'june' ? 'july' : (world.month == 'july' ? 'august' : (world.month == 'august' ? 'september' : (world.month == 'september' ? 'october' : (world.month == 'october' ? 'november' : (world.month == 'november' ? 'december' : (world.month == 'december' ? 'january' : (world.month == 'january' ? 'february' : (world.month == 'february' ? 'march' : world.month)))))))))))) : world.month }}"` · say "Arrived at Willamette Valley." |
| 2 | [`continue`](#intent-continue) | `world.food_lbs - (world.rations == 'bare_bones' ? 6 : (world.rations == 'meager' ? 8 : 10)) <= 0` | [`ended_lost`](#room-ended-lost) | set `day = "{{ world.day + 1 > 30 ? 1 : world.day + 1 }}"`, `food_lbs = 0`, `year = "{{ world.day + 1 > 30 && world.month == 'december' ? world.year + 1 : world.year }}"` · set `month = "{{ world.day + 1 > 30 ? (world.month == 'march' ? 'april' : (world.month == 'april' ? 'may' : (world.month == 'may' ? 'june' : (world.month == 'june' ? 'july' : (world.month == 'july' ? 'august' : (world.month == 'august' ? 'september' : (world.month == 'september' ? 'october' : (world.month == 'october' ? 'november' : (world.month == 'november' ? 'december' : (world.month == 'december' ? 'january' : (world.month == 'january' ? 'february' : (world.month == 'february' ? 'march' : world.month)))))))))))) : world.month }}"` · say "The party has run out of food between Snake River Crossing and Willamette Valley. They starve on the trail." |
| 3 | [`continue`](#intent-continue) | `int(world.miles_traveled + world.rng_counter) % 100 >= 60 && int(world.miles_traveled + world.rng_counter) % 100 < 75` | `../event_disease` | set `rng_last = "{{ int(world.miles_traveled + world.rng_counter) % 100 }}"` · set `day = "{{ world.day + 1 > 30 ? 1 : world.day + 1 }}"`, `event_kind = "disease"`, `food_lbs = "{{ world.food_lbs - (world.rations == 'bare_bones' ? 6 : (world.rations == 'meager' ? 8 : 10)) }}"`, `miles_traveled = "{{ world.miles_traveled + int((world.pace == 'grueling' ? 22 : (world.pace == 'strenuous' ? 18 : 14)) * (world.month == 'december' || world.month == 'january' || world.month == 'february' ? 70 : (world.month == 'november' ? 80 : (world.month == 'march' || world.month == 'october' ? 85 : (world.month == 'april' || world.month == 'september' ? 95 : 100)))) / 100) }}"`, `year = "{{ world.day + 1 > 30 && world.month == 'december' ? world.year + 1 : world.year }}"` · set `month = "{{ world.day + 1 > 30 ? (world.month == 'march' ? 'april' : (world.month == 'april' ? 'may' : (world.month == 'may' ? 'june' : (world.month == 'june' ? 'july' : (world.month == 'july' ? 'august' : (world.month == 'august' ? 'september' : (world.month == 'september' ? 'october' : (world.month == 'october' ? 'november' : (world.month == 'november' ? 'december' : (world.month == 'december' ? 'january' : (world.month == 'january' ? 'february' : (world.month == 'february' ? 'march' : world.month)))))))))))) : world.month }}"` · increment `rng_counter += 1` |
| 4 | [`continue`](#intent-continue) | `int(world.miles_traveled + world.rng_counter) % 100 >= 75 && int(world.miles_traveled + world.rng_counter) % 100 < 85` | `../event_breakdown` | set `rng_last = "{{ int(world.miles_traveled + world.rng_counter) % 100 }}"` · set `day = "{{ world.day + 1 > 30 ? 1 : world.day + 1 }}"`, `event_kind = "breakdown"`, `food_lbs = "{{ world.food_lbs - (world.rations == 'bare_bones' ? 6 : (world.rations == 'meager' ? 8 : 10)) }}"`, `miles_traveled = "{{ world.miles_traveled + int((world.pace == 'grueling' ? 22 : (world.pace == 'strenuous' ? 18 : 14)) * (world.month == 'december' || world.month == 'january' || world.month == 'february' ? 70 : (world.month == 'november' ? 80 : (world.month == 'march' || world.month == 'october' ? 85 : (world.month == 'april' || world.month == 'september' ? 95 : 100)))) / 100) }}"`, `year = "{{ world.day + 1 > 30 && world.month == 'december' ? world.year + 1 : world.year }}"` · set `month = "{{ world.day + 1 > 30 ? (world.month == 'march' ? 'april' : (world.month == 'april' ? 'may' : (world.month == 'may' ? 'june' : (world.month == 'june' ? 'july' : (world.month == 'july' ? 'august' : (world.month == 'august' ? 'september' : (world.month == 'september' ? 'october' : (world.month == 'october' ? 'november' : (world.month == 'november' ? 'december' : (world.month == 'december' ? 'january' : (world.month == 'january' ? 'february' : (world.month == 'february' ? 'march' : world.month)))))))))))) : world.month }}"` · increment `rng_counter += 1` |
| 5 | [`continue`](#intent-continue) | `int(world.miles_traveled + world.rng_counter) % 100 >= 85 && int(world.miles_traveled + world.rng_counter) % 100 < 92` | `../event_weather` | set `rng_last = "{{ int(world.miles_traveled + world.rng_counter) % 100 }}"` · set `day = "{{ world.day + 1 > 30 ? 1 : world.day + 1 }}"`, `event_kind = "weather"`, `food_lbs = "{{ world.food_lbs - (world.rations == 'bare_bones' ? 6 : (world.rations == 'meager' ? 8 : 10)) }}"`, `miles_traveled = "{{ world.miles_traveled + int((world.pace == 'grueling' ? 22 : (world.pace == 'strenuous' ? 18 : 14)) * (world.month == 'december' || world.month == 'january' || world.month == 'february' ? 70 : (world.month == 'november' ? 80 : (world.month == 'march' || world.month == 'october' ? 85 : (world.month == 'april' || world.month == 'september' ? 95 : 100)))) / 100) }}"`, `year = "{{ world.day + 1 > 30 && world.month == 'december' ? world.year + 1 : world.year }}"` · set `month = "{{ world.day + 1 > 30 ? (world.month == 'march' ? 'april' : (world.month == 'april' ? 'may' : (world.month == 'may' ? 'june' : (world.month == 'june' ? 'july' : (world.month == 'july' ? 'august' : (world.month == 'august' ? 'september' : (world.month == 'september' ? 'october' : (world.month == 'october' ? 'november' : (world.month == 'november' ? 'december' : (world.month == 'december' ? 'january' : (world.month == 'january' ? 'february' : (world.month == 'february' ? 'march' : world.month)))))))))))) : world.month }}"` · increment `rng_counter += 1` |
| 6 | [`continue`](#intent-continue) | `int(world.miles_traveled + world.rng_counter) % 100 >= 92 && int(world.miles_traveled + world.rng_counter) % 100 < 97` | `../event_encounter` | set `rng_last = "{{ int(world.miles_traveled + world.rng_counter) % 100 }}"` · set `day = "{{ world.day + 1 > 30 ? 1 : world.day + 1 }}"`, `event_kind = "encounter"`, `food_lbs = "{{ world.food_lbs - (world.rations == 'bare_bones' ? 6 : (world.rations == 'meager' ? 8 : 10)) }}"`, `miles_traveled = "{{ world.miles_traveled + int((world.pace == 'grueling' ? 22 : (world.pace == 'strenuous' ? 18 : 14)) * (world.month == 'december' || world.month == 'january' || world.month == 'february' ? 70 : (world.month == 'november' ? 80 : (world.month == 'march' || world.month == 'october' ? 85 : (world.month == 'april' || world.month == 'september' ? 95 : 100)))) / 100) }}"`, `year = "{{ world.day + 1 > 30 && world.month == 'december' ? world.year + 1 : world.year }}"` · set `month = "{{ world.day + 1 > 30 ? (world.month == 'march' ? 'april' : (world.month == 'april' ? 'may' : (world.month == 'may' ? 'june' : (world.month == 'june' ? 'july' : (world.month == 'july' ? 'august' : (world.month == 'august' ? 'september' : (world.month == 'september' ? 'october' : (world.month == 'october' ? 'november' : (world.month == 'november' ? 'december' : (world.month == 'december' ? 'january' : (world.month == 'january' ? 'february' : (world.month == 'february' ? 'march' : world.month)))))))))))) : world.month }}"` · increment `rng_counter += 1` |
| 7 | [`continue`](#intent-continue) | `int(world.miles_traveled + world.rng_counter) % 100 >= 97` | `../event_supply_loss` | set `rng_last = "{{ int(world.miles_traveled + world.rng_counter) % 100 }}"` · set `day = "{{ world.day + 1 > 30 ? 1 : world.day + 1 }}"`, `event_kind = "supply_loss"`, `food_lbs = "{{ world.food_lbs - (world.rations == 'bare_bones' ? 6 : (world.rations == 'meager' ? 8 : 10)) }}"`, `miles_traveled = "{{ world.miles_traveled + int((world.pace == 'grueling' ? 22 : (world.pace == 'strenuous' ? 18 : 14)) * (world.month == 'december' || world.month == 'january' || world.month == 'february' ? 70 : (world.month == 'november' ? 80 : (world.month == 'march' || world.month == 'october' ? 85 : (world.month == 'april' || world.month == 'september' ? 95 : 100)))) / 100) }}"`, `year = "{{ world.day + 1 > 30 && world.month == 'december' ? world.year + 1 : world.year }}"` · set `month = "{{ world.day + 1 > 30 ? (world.month == 'march' ? 'april' : (world.month == 'april' ? 'may' : (world.month == 'may' ? 'june' : (world.month == 'june' ? 'july' : (world.month == 'july' ? 'august' : (world.month == 'august' ? 'september' : (world.month == 'september' ? 'october' : (world.month == 'october' ? 'november' : (world.month == 'november' ? 'december' : (world.month == 'december' ? 'january' : (world.month == 'january' ? 'february' : (world.month == 'february' ? 'march' : world.month)))))))))))) : world.month }}"` · increment `rng_counter += 1` |
| 8 | [`continue`](#intent-continue) | _default_ | `.` | set `rng_last = "{{ int(world.miles_traveled + world.rng_counter) % 100 }}"` · set `day = "{{ world.day + 1 > 30 ? 1 : world.day + 1 }}"`, `event_kind = ""`, `food_lbs = "{{ world.food_lbs - (world.rations == 'bare_bones' ? 6 : (world.rations == 'meager' ? 8 : 10)) }}"`, `miles_traveled = "{{ world.miles_traveled + int((world.pace == 'grueling' ? 22 : (world.pace == 'strenuous' ? 18 : 14)) * (world.month == 'december' || world.month == 'january' || world.month == 'february' ? 70 : (world.month == 'november' ? 80 : (world.month == 'march' || world.month == 'october' ? 85 : (world.month == 'april' || world.month == 'september' ? 95 : 100)))) / 100) }}"`, `year = "{{ world.day + 1 > 30 && world.month == 'december' ? world.year + 1 : world.year }}"` · set `month = "{{ world.day + 1 > 30 ? (world.month == 'march' ? 'april' : (world.month == 'april' ? 'may' : (world.month == 'may' ? 'june' : (world.month == 'june' ? 'july' : (world.month == 'july' ? 'august' : (world.month == 'august' ? 'september' : (world.month == 'september' ? 'october' : (world.month == 'october' ? 'november' : (world.month == 'november' ? 'december' : (world.month == 'december' ? 'january' : (world.month == 'january' ? 'february' : (world.month == 'february' ? 'march' : world.month)))))))))))) : world.month }}"` · increment `rng_counter += 1` |
| 9 | [`look`](#intent-look) |  | `.` |  |
| 10 | [`quit`](#intent-quit) |  | [`ended_lost`](#room-ended-lost) | say "The party abandons the journey between Snake River Crossing and Willamette Valley." |
| 11 | [`set_pace`](#intent-set-pace) |  | `.` | set `pace = "{{ slots.pace }}"` · say "Pace set to {{ slots.pace }}." |
| 12 | [`set_rations`](#intent-set-rations) |  | `.` | set `rations = "{{ slots.rations }}"` · say "Rations set to {{ slots.rations }}." |

### <a id="room-rest-room"></a> `rest_room`  _(compound)_

Make camp to rest the party.

**Initial child**: `rest_idle`

**Shows world**: `day`, `health_avg`, `food_lbs`, `party_alive`, `last_job_id`, `current_landmark`

### <a id="room-rest-room-rest-done"></a> `rest_room.rest_done`

Camp broken; back on the trail.

**Shows world**: `health_avg`, `food_lbs`, `day`, `current_landmark`

**Transitions**:

| # | Intent | Guard | → | Effects |
|---|---|---|---|---|
| 1 | [`continue`](#intent-continue) | `world.current_landmark == 'Kansas River Crossing'` | [`leg_a_awaiting_reply`](#room-leg-a-awaiting-reply) |  |
| 2 | [`continue`](#intent-continue) | `world.current_landmark == 'Fort Kearney'` | [`leg_b_awaiting_reply`](#room-leg-b-awaiting-reply) |  |
| 3 | [`continue`](#intent-continue) | `world.current_landmark == 'Chimney Rock'` | [`leg_c_awaiting_reply`](#room-leg-c-awaiting-reply) |  |
| 4 | [`continue`](#intent-continue) | `world.current_landmark == 'Fort Laramie'` | [`leg_d_awaiting_reply`](#room-leg-d-awaiting-reply) |  |
| 5 | [`continue`](#intent-continue) | `world.current_landmark == 'South Pass'` | [`leg_e_awaiting_reply`](#room-leg-e-awaiting-reply) |  |
| 6 | [`continue`](#intent-continue) | `world.current_landmark == 'Snake River Crossing'` | [`leg_f_awaiting_reply`](#room-leg-f-awaiting-reply) |  |
| 7 | [`continue`](#intent-continue) | `world.current_landmark == 'Willamette Valley'` | [`leg_g_awaiting_reply`](#room-leg-g-awaiting-reply) |  |
| 8 | [`continue`](#intent-continue) | _default_ | [`leg_a_awaiting_reply`](#room-leg-a-awaiting-reply) | _hint: Unknown landmark — returning to the first leg._ |
| 9 | [`look`](#intent-look) |  | `.` |  |

### <a id="room-rest-room-rest-idle"></a> `rest_room.rest_idle`

Choosing how many days to rest.

**Transitions**:

| # | Intent | Guard | → | Effects |
|---|---|---|---|---|
| 1 | [`look`](#intent-look) |  | `.` |  |
| 2 | [`rest`](#intent-rest) | `int(slots.days) > 0` | `../rest_running` | set `pending_rest_days = "{{ int(slots.days) }}"` · say "Resting {{ slots.days }} day(s)." |
| 3 | [`rest`](#intent-rest) | _default_ | `.` | _hint: Need at least 1 day to rest._ |

### <a id="room-rest-room-rest-running"></a> `rest_room.rest_running`

Camp set; party resting.

**On enter**:

1. set `last_job_originating_state = "rest_room.rest_running"`
2. invoke `host.run` with `cmd = "sleep 0.2"`, bind `last_job_id ← job_id`

**Transitions**:

| # | Intent | Guard | → | Effects |
|---|---|---|---|---|
| 1 | [`continue`](#intent-continue) |  | `../rest_done` | _hint: Walk to rest_done; on_complete should have already settled the world._ |
| 2 | [`look`](#intent-look) |  | `.` |  |

### <a id="room-river-crossing"></a> `river_crossing`  _(compound)_

At the river ({{ world.current_landmark }}).

**Initial child**: `{% if world.river_depth_ft < 3 %}shallow{% else %}{% if world.river_depth_ft < 6 %}mid{% else %}deep{% endif %}{% endif %}`

**Shows world**: `current_landmark`, `river_depth_ft`, `river_width_ft`, `money`, `food_lbs`, `oxen`, `party_alive`, `miles_traveled`, `crossing_method`, `crossing_confidence`, `river_outcome`, `last_job_id`

### <a id="room-river-crossing-deep"></a> `river_crossing.deep`

High water at {{ world.current_landmark }} — {{ world.river_depth_ft }} ft.

**Transitions**:

| # | Intent | Guard | → | Effects |
|---|---|---|---|---|
| 1 | [`cancel_crossing`](#intent-cancel-crossing) | `world.current_landmark == 'Kansas River Crossing'` | [`leg_a_awaiting_reply`](#room-leg-a-awaiting-reply) |  |
| 2 | [`cancel_crossing`](#intent-cancel-crossing) | `world.current_landmark == 'Snake River Crossing'` | [`leg_f_awaiting_reply`](#room-leg-f-awaiting-reply) |  |
| 3 | [`cancel_crossing`](#intent-cancel-crossing) | _default_ | [`ended_lost`](#room-ended-lost) | _hint: Unknown river — ending the run._ |
| 4 | [`look`](#intent-look) |  | `.` |  |
| 5 | [`propose_crossing`](#intent-propose-crossing) |  | `../reviewing` | set `crossing_confidence = "{{ int(slots.confidence) }}"`, `crossing_method = "{{ slots.method }}"` · say "The wagon master walks the bank, looks at the team, looks at the water. 'We'll {{ slots.method }} her — confidence about {{ slots.confidence }} in ten.'" |

### <a id="room-river-crossing-executing"></a> `river_crossing.executing`

The wagon is in the water at {{ world.current_landmark }}.

**Shows world**: `crossing_method`, `river_outcome`, `last_job_id`, `river_depth_ft`, `oxen`, `miles_traveled`

**On enter**:

1. invoke `host.run` with `cmd = "sleep 0.2; echo crossed"`, bind `last_job_id ← job_id`

**Transitions**:

| # | Intent | Guard | → | Effects |
|---|---|---|---|---|
| 1 | [`continue`](#intent-continue) | `world.river_outcome == 'crossed' && world.current_landmark == 'Kansas River Crossing'` | [`leg_b_executing`](#room-leg-b-executing) | set `crossing_confidence = 0`, `crossing_method = "none"`, `current_landmark = "Kansas River Crossing"`, `miles_traveled = 0`, `river_outcome = ""` · say "Across! The team finds its footing on the far bank and we hitch the load tight again — Fort Kearney lies ahead." |
| 2 | [`continue`](#intent-continue) | `world.river_outcome == 'crossed' && world.current_landmark == 'Snake River Crossing'` | [`leg_g_executing`](#room-leg-g-executing) | set `crossing_confidence = 0`, `crossing_method = "none"`, `current_landmark = "Snake River Crossing"`, `miles_traveled = 0`, `river_outcome = ""` · say "Across! The Snake is behind us, the team is dripping but whole, and the road bends west toward the Willamette." |
| 3 | [`continue`](#intent-continue) | `world.river_outcome == 'swept_supplies' && world.river_depth_ft < 3` | `../shallow` | set `crossing_confidence = 0`, `crossing_method = "none"`, `food_lbs = "{{ world.food_lbs - 100 }}"`, `river_outcome = ""` · say "The current took the load off the wagon-bed — 100 lbs of stores gone downriver. The wagon is dragged back to dry ground; we'll have to try again." |
| 4 | [`continue`](#intent-continue) | `world.river_outcome == 'swept_supplies' && world.river_depth_ft < 6` | `../mid` | set `crossing_confidence = 0`, `crossing_method = "none"`, `food_lbs = "{{ world.food_lbs - 100 }}"`, `river_outcome = ""` · say "The current took the load off the wagon-bed — 100 lbs of stores gone downriver. The wagon is dragged back to dry ground; we'll have to try again." |
| 5 | [`continue`](#intent-continue) | `world.river_outcome == 'swept_supplies'` | `../deep` | set `crossing_confidence = 0`, `crossing_method = "none"`, `food_lbs = "{{ world.food_lbs - 100 }}"`, `river_outcome = ""` · say "The current took the load off the wagon-bed — 100 lbs of stores gone downriver. The wagon is dragged back to dry ground; we'll have to try again." |
| 6 | [`continue`](#intent-continue) | `world.river_outcome == 'drowned' && world.river_depth_ft < 3` | `../shallow` | set `crossing_confidence = 0`, `crossing_method = "none"`, `party_alive = "{{ world.party_alive - 1 }}"`, `river_outcome = ""` · say "One of the party was taken in the crossing — pulled under in the current and lost before the team could come about. The wagon is dragged back to the bank in silence." |
| 7 | [`continue`](#intent-continue) | `world.river_outcome == 'drowned' && world.river_depth_ft < 6` | `../mid` | set `crossing_confidence = 0`, `crossing_method = "none"`, `party_alive = "{{ world.party_alive - 1 }}"`, `river_outcome = ""` · say "One of the party was taken in the crossing — pulled under in the current and lost before the team could come about. The wagon is dragged back to the bank in silence." |
| 8 | [`continue`](#intent-continue) | `world.river_outcome == 'drowned'` | `../deep` | set `crossing_confidence = 0`, `crossing_method = "none"`, `party_alive = "{{ world.party_alive - 1 }}"`, `river_outcome = ""` · say "One of the party was taken in the crossing — pulled under in the current and lost before the team could come about. The wagon is dragged back to the bank in silence." |
| 9 | [`continue`](#intent-continue) | _default_ | `.` | _hint: The wagon's still mid-stream. Wait for word from the bank._ |
| 10 | [`look`](#intent-look) |  | `.` |  |

### <a id="room-river-crossing-mid"></a> `river_crossing.mid`

Fair water at {{ world.current_landmark }} — {{ world.river_depth_ft }} ft.

**Transitions**:

| # | Intent | Guard | → | Effects |
|---|---|---|---|---|
| 1 | [`cancel_crossing`](#intent-cancel-crossing) | `world.current_landmark == 'Kansas River Crossing'` | [`leg_a_awaiting_reply`](#room-leg-a-awaiting-reply) |  |
| 2 | [`cancel_crossing`](#intent-cancel-crossing) | `world.current_landmark == 'Snake River Crossing'` | [`leg_f_awaiting_reply`](#room-leg-f-awaiting-reply) |  |
| 3 | [`cancel_crossing`](#intent-cancel-crossing) | _default_ | [`ended_lost`](#room-ended-lost) | _hint: Unknown river — ending the run._ |
| 4 | [`look`](#intent-look) |  | `.` |  |
| 5 | [`propose_crossing`](#intent-propose-crossing) |  | `../reviewing` | set `crossing_confidence = "{{ int(slots.confidence) }}"`, `crossing_method = "{{ slots.method }}"` · say "The wagon master walks the bank, looks at the team, looks at the water. 'We'll {{ slots.method }} her — confidence about {{ slots.confidence }} in ten.'" |

### <a id="room-river-crossing-reviewing"></a> `river_crossing.reviewing`

The wagon master spells out the plan.

**Shows world**: `crossing_method`, `crossing_confidence`, `current_landmark`, `river_depth_ft`

**Transitions**:

| # | Intent | Guard | → | Effects |
|---|---|---|---|---|
| 1 | [`accept_crossing`](#intent-accept-crossing) |  | `../executing` | set `river_outcome = ""` · say "The wagon master nods to the team. The lead ox steps into the water; the wheels follow." |
| 2 | [`cancel_crossing`](#intent-cancel-crossing) | `world.current_landmark == 'Kansas River Crossing'` | [`leg_a_awaiting_reply`](#room-leg-a-awaiting-reply) | set `crossing_confidence = 0`, `crossing_method = "none"` |
| 3 | [`cancel_crossing`](#intent-cancel-crossing) | `world.current_landmark == 'Snake River Crossing'` | [`leg_f_awaiting_reply`](#room-leg-f-awaiting-reply) | set `crossing_confidence = 0`, `crossing_method = "none"` |
| 4 | [`cancel_crossing`](#intent-cancel-crossing) | _default_ | [`ended_lost`](#room-ended-lost) | _hint: Unknown river — ending the run._ |
| 5 | [`look`](#intent-look) |  | `.` |  |
| 6 | [`refine_crossing`](#intent-refine-crossing) | `world.river_depth_ft < 3` | `../shallow` | set `crossing_confidence = 0`, `crossing_method = "none"` |
| 7 | [`refine_crossing`](#intent-refine-crossing) | `world.river_depth_ft < 6` | `../mid` | set `crossing_confidence = 0`, `crossing_method = "none"` |
| 8 | [`refine_crossing`](#intent-refine-crossing) | _default_ | `../deep` | set `crossing_confidence = 0`, `crossing_method = "none"` |

### <a id="room-river-crossing-shallow"></a> `river_crossing.shallow`

Low water at {{ world.current_landmark }} — {{ world.river_depth_ft }} ft deep.

**Transitions**:

| # | Intent | Guard | → | Effects |
|---|---|---|---|---|
| 1 | [`cancel_crossing`](#intent-cancel-crossing) | `world.current_landmark == 'Kansas River Crossing'` | [`leg_a_awaiting_reply`](#room-leg-a-awaiting-reply) | say "The team backs off the cutbank. The river runs on without us; we'll come back to it." |
| 2 | [`cancel_crossing`](#intent-cancel-crossing) | `world.current_landmark == 'Snake River Crossing'` | [`leg_f_awaiting_reply`](#room-leg-f-awaiting-reply) | say "The team backs off the cutbank. The river runs on without us; we'll come back to it." |
| 3 | [`cancel_crossing`](#intent-cancel-crossing) | _default_ | [`ended_lost`](#room-ended-lost) | _hint: Unknown river — ending the run._ |
| 4 | [`look`](#intent-look) |  | `.` |  |
| 5 | [`propose_crossing`](#intent-propose-crossing) |  | `../reviewing` | set `crossing_confidence = "{{ int(slots.confidence) }}"`, `crossing_method = "{{ slots.method }}"` · say "The wagon master walks the bank, looks at the team, looks at the water. 'We'll {{ slots.method }} her — confidence about {{ slots.confidence }} in ten.'" |

### <a id="room-robbery-aftermath"></a> `robbery_aftermath`

Back on the trail after the bandit encounter.

**Shows world**: `money`, `party_alive`, `last_event`, `current_landmark`

**Transitions**:

| # | Intent | Guard | → | Effects |
|---|---|---|---|---|
| 1 | [`continue`](#intent-continue) | `world.current_landmark == 'Kansas River Crossing'` | [`leg_a_awaiting_reply`](#room-leg-a-awaiting-reply) |  |
| 2 | [`continue`](#intent-continue) | `world.current_landmark == 'Fort Kearney'` | [`leg_b_awaiting_reply`](#room-leg-b-awaiting-reply) |  |
| 3 | [`continue`](#intent-continue) | `world.current_landmark == 'Chimney Rock'` | [`leg_c_awaiting_reply`](#room-leg-c-awaiting-reply) |  |
| 4 | [`continue`](#intent-continue) | `world.current_landmark == 'Fort Laramie'` | [`leg_d_awaiting_reply`](#room-leg-d-awaiting-reply) |  |
| 5 | [`continue`](#intent-continue) | `world.current_landmark == 'South Pass'` | [`leg_e_awaiting_reply`](#room-leg-e-awaiting-reply) |  |
| 6 | [`continue`](#intent-continue) | `world.current_landmark == 'Snake River Crossing'` | [`leg_f_awaiting_reply`](#room-leg-f-awaiting-reply) |  |
| 7 | [`continue`](#intent-continue) | `world.current_landmark == 'Willamette Valley'` | [`leg_g_awaiting_reply`](#room-leg-g-awaiting-reply) |  |
| 8 | [`continue`](#intent-continue) | _default_ | [`leg_a_awaiting_reply`](#room-leg-a-awaiting-reply) | _hint: Unknown landmark — returning to the first leg._ |
| 9 | [`look`](#intent-look) |  | `.` |  |

### <a id="room-snow-blocked"></a> `snow_blocked`

South Pass is snowed in — wait for spring or turn back.

**Shows world**: `month`, `day`, `year`, `food_lbs`, `party_alive`, `oxen`, `health_avg`

**Transitions**:

| # | Intent | Guard | → | Effects |
|---|---|---|---|---|
| 1 | [`give_up`](#intent-give-up) |  | [`ended_lost`](#room-ended-lost) | say "The party turns back east, defeated by the snow. The journey ends short of Oregon." |
| 2 | [`look`](#intent-look) |  | `.` |  |
| 3 | [`wait_for_spring`](#intent-wait-for-spring) | `world.food_lbs - 120 <= 0` | [`ended_lost`](#room-ended-lost) | set `day = "{{ world.day + 30 }}"`, `food_lbs = 0` · say "The food ran out before spring. The party starves in the snow." |
| 4 | [`wait_for_spring`](#intent-wait-for-spring) | `world.month == 'march'` | [`leg_e_awaiting_reply`](#room-leg-e-awaiting-reply) | set `day = 1`, `food_lbs = "{{ world.food_lbs - 120 }}"`, `health_avg = "{{ world.health_avg - 5 }}"`, `month = "april"` · say "Spring arrives. The pass is open." |
| 5 | [`wait_for_spring`](#intent-wait-for-spring) | _default_ | `.` | set `day = 1`, `food_lbs = "{{ world.food_lbs - 120 }}"`, `health_avg = "{{ world.health_avg - 5 }}"`, `year = "{{ world.month == 'december' ? world.year + 1 : world.year }}"` · set `month = "{{ world.month == 'october' ? 'november' :\n   (world.month == 'november' ? 'december' :\n   (world.month == 'december' ? 'january' :\n   (world.month == 'january' ? 'february' :\n   (world.month == 'february' ? 'march' : world.month)))) }}\n"` · say "Another month of winter camp passes." |

### <a id="room-trail-guide"></a> `trail_guide`  _(compound)_

Wagon master — persistent chats keyed by profession.

**Initial child**: `trail_guide_list`

**Shows world**: `wagon_chats_view`, `wagon_chat_count`, `wagon_chat_id`, `wagon_chat_title`, `wagon_answer`, `wagon_chat_turns`, `profession`

### <a id="room-trail-guide-trail-guide-active"></a> `trail_guide.trail_guide_active`  _(mode: conversational)_

Wagon master — active chat. Ask another question or go back.

**Shows world**: `wagon_chat_id`, `wagon_chat_title`, `wagon_question`, `wagon_answer`, `wagon_chat_turns`

**On enter**:

1. invoke `host.agent.converse` with `agent = "wagon_master"`, `chat_id = "{{ world.wagon_chat_id }}"`, `question = "{{ world.wagon_question }}"`, bind `wagon_answer ← answer`, `wagon_chat_id ← chat_id`, `wagon_session_id ← claude_session_id`
2. invoke `host.chat.suggest_title` with `chat_id = "{{ world.wagon_chat_id }}"`, `force = false`, bind `wagon_chat_title ← title`

**Transitions**:

| # | Intent | Guard | → | Effects |
|---|---|---|---|---|
| 1 | [`ask_question`](#intent-ask-question) |  | [`trail_guide.trail_guide_active`](#room-trail-guide-trail-guide-active) | set `wagon_answer = ""`, `wagon_question = "{{ slots.question }}"` · increment `wagon_chat_turns += 1` · invoke `host.agent.converse` with `agent = "wagon_master"`, `chat_id = "{{ world.wagon_chat_id }}"`, `question = "{{ slots.question }}"`, bind `wagon_answer ← answer`, `wagon_chat_id ← chat_id`, `wagon_session_id ← claude_session_id` · invoke `host.chat.suggest_title` with `chat_id = "{{ world.wagon_chat_id }}"`, `force = false`, bind `wagon_chat_title ← title` |
| 2 | [`back`](#intent-back) |  | [`trail_guide.trail_guide_list`](#room-trail-guide-trail-guide-list) |  |
| 3 | [`look`](#intent-look) |  | `.` |  |

### <a id="room-trail-guide-trail-guide-active-new"></a> `trail_guide.trail_guide_active_new`  _(mode: conversational)_

Wagon master — starting a fresh chat.

**Shows world**: `wagon_chat_id`, `wagon_chat_title`, `wagon_question`, `wagon_answer`, `wagon_chat_turns`

**On enter**:

1. invoke `host.chat.create` with `app = "oregon-trail"`, `room = "trail_guide"`, `scope_key = "{{ world.profession }}"`, `title = "Question about the trail"`, bind `wagon_chat_id ← chat_id`, `wagon_chat_title ← title`
2. invoke `host.agent.converse` with `agent = "wagon_master"`, `chat_id = "{{ world.wagon_chat_id }}"`, `question = "{{ world.wagon_question }}"`, bind `wagon_answer ← answer`, `wagon_chat_id ← chat_id`, `wagon_session_id ← claude_session_id`
3. invoke `host.chat.suggest_title` with `chat_id = "{{ world.wagon_chat_id }}"`, `force = false`, bind `wagon_chat_title ← title`

**Transitions**:

| # | Intent | Guard | → | Effects |
|---|---|---|---|---|
| 1 | [`ask_question`](#intent-ask-question) |  | [`trail_guide.trail_guide_active`](#room-trail-guide-trail-guide-active) | set `wagon_answer = ""`, `wagon_question = "{{ slots.question }}"` · increment `wagon_chat_turns += 1` |
| 2 | [`back`](#intent-back) |  | [`trail_guide.trail_guide_list`](#room-trail-guide-trail-guide-list) |  |
| 3 | [`look`](#intent-look) |  | `.` |  |

### <a id="room-trail-guide-trail-guide-list"></a> `trail_guide.trail_guide_list`  _(mode: conversational)_

Wagon-master chats — list, open, rename, archive, fork, or start fresh.

**Shows world**: `wagon_chats_view`, `wagon_chat_count`, `profession`

**On enter**:

1. invoke `host.chat.list` with `app = "oregon-trail"`, `room = "trail_guide"`, `scope_key = "{{ world.profession }}"`, bind `wagon_chat_count ← count`, `wagon_chats_view ← rendered`

**Transitions**:

| # | Intent | Guard | → | Effects |
|---|---|---|---|---|
| 1 | [`archive_chat`](#intent-archive-chat) |  | [`trail_guide.trail_guide_list`](#room-trail-guide-trail-guide-list) | invoke `host.chat.resolve_ref` with `app = "oregon-trail"`, `ref = "{{ slots.chat_id }}"`, `room = "trail_guide"`, `scope_key = "{{ world.profession }}"`, bind `wagon_chat_id ← chat_id` · invoke `host.chat.archive` with `chat_id = "{{ world.wagon_chat_id }}"` |
| 2 | [`ask_question`](#intent-ask-question) |  | [`trail_guide.trail_guide_active_new`](#room-trail-guide-trail-guide-active-new) | set `wagon_answer = ""`, `wagon_chat_id = ""`, `wagon_chat_turns = 1`, `wagon_question = "{{ slots.question }}"` |
| 3 | [`back`](#intent-back) |  | `{{ world.last_job_originating_state }}` | _(no-history)_ |
| 4 | [`fork_chat`](#intent-fork-chat) |  | [`trail_guide.trail_guide_active`](#room-trail-guide-trail-guide-active) | invoke `host.chat.resolve_ref` with `app = "oregon-trail"`, `ref = "{{ slots.chat_id }}"`, `room = "trail_guide"`, `scope_key = "{{ world.profession }}"`, bind `wagon_chat_id ← chat_id` · invoke `host.chat.fork` with `chat_id = "{{ world.wagon_chat_id }}"`, `title = "{{ slots.title }}"`, bind `wagon_chat_id ← chat_id`, `wagon_chat_title ← title` · set `wagon_answer = ""`, `wagon_chat_turns = 0`, `wagon_question = ""` |
| 5 | [`look`](#intent-look) |  | `.` |  |
| 6 | [`open_chat`](#intent-open-chat) |  | [`trail_guide.trail_guide_active`](#room-trail-guide-trail-guide-active) | invoke `host.chat.resolve_ref` with `app = "oregon-trail"`, `ref = "{{ slots.chat_id }}"`, `room = "trail_guide"`, `scope_key = "{{ world.profession }}"`, bind `wagon_chat_id ← chat_id`, `wagon_chat_title ← title` · set `wagon_answer = ""`, `wagon_chat_turns = 0`, `wagon_question = ""` |
| 7 | [`rename_chat`](#intent-rename-chat) |  | [`trail_guide.trail_guide_list`](#room-trail-guide-trail-guide-list) | invoke `host.chat.resolve_ref` with `app = "oregon-trail"`, `ref = "{{ slots.chat_id }}"`, `room = "trail_guide"`, `scope_key = "{{ world.profession }}"`, bind `wagon_chat_id ← chat_id` · invoke `host.chat.rename` with `chat_id = "{{ world.wagon_chat_id }}"`, `title = "{{ slots.title }}"` |

### <a id="room-world-clock"></a> `world_clock`  _(parallel)_

Two orthogonal regions: weather + calendar.

### <a id="room-world-clock-calendar"></a> `world_clock.calendar`  _(compound)_

**Initial child**: `day_active`

**Transitions**:

| # | Intent | Guard | → | Effects |
|---|---|---|---|---|
| 1 | [`precip_heavy`](#intent-precip-heavy) |  | [`world_clock.calendar.day_active`](#room-world-clock-calendar-day-active) | set `precip_observed = true` |
| 2 | [`snow_starts`](#intent-snow-starts) |  | [`world_clock.calendar.day_active`](#room-world-clock-calendar-day-active) | set `snow_observed = true` |

### <a id="room-world-clock-calendar-day-active"></a> `world_clock.calendar.day_active`

### <a id="room-world-clock-weather"></a> `world_clock.weather`  _(compound)_

**Initial child**: `dry`

### <a id="room-world-clock-weather-dry"></a> `world_clock.weather.dry`

**Transitions**:

| # | Intent | Guard | → | Effects |
|---|---|---|---|---|
| 1 | [`weather_advance`](#intent-weather-advance) |  | [`world_clock.weather.rain`](#room-world-clock-weather-rain) | set `day = "{{ world.day + 1 }}"`, `weather_kind = "rain"` · emit `precip_heavy` |

### <a id="room-world-clock-weather-rain"></a> `world_clock.weather.rain`

**Transitions**:

| # | Intent | Guard | → | Effects |
|---|---|---|---|---|
| 1 | [`weather_advance`](#intent-weather-advance) |  | [`world_clock.weather.snow`](#room-world-clock-weather-snow) | set `day = "{{ world.day + 1 }}"`, `weather_kind = "snow"` · emit `snow_starts` |

### <a id="room-world-clock-weather-snow"></a> `world_clock.weather.snow`

**Transitions**:

| # | Intent | Guard | → | Effects |
|---|---|---|---|---|
| 1 | [`weather_advance`](#intent-weather-advance) |  | [`world_clock.weather.dry`](#room-world-clock-weather-dry) | set `day = "{{ world.day + 1 }}"`, `weather_kind = "dry"` |

## Off-path Escape Hatch

- Trigger: `/freeform`
- Banner: "*** off the trail — answers do not affect your journey ***"
- Return: `/onpath`

---

_Generated from `app.yaml` by `kitsoki render`. Do not edit this file directly — edit `app.yaml` and re-run `kitsoki render`. See `kitsoki docs apply-proposal` for the LLM-driven proposal workflow._
