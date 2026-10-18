"use strict";
(() => {
  const $ = (id) => document.getElementById(id);
  const automaticControl = $("auto-refresh");
  const refreshControl = $("refresh");
  const fields = [$("room-number"), $("phase"), $("visibility"), $("game-id")];
  const numberFormat = new Intl.NumberFormat("ko-KR");
  let generation = 0, active = null, timer = null, displayTimer = null;
  let automatic = true, locked = false, pendingRefresh = false, pageActive = true, visibleResumed = true, offset = 0;
  let statusData = null, roomsData = null;
  let statusFailed = false, roomsFailed = false;
  const rowFreshness = [];

  function setText(id, value) { $(id).textContent = value; }
  function query() {
    return {...(fields[3].value ? {game_id: fields[3].value} : {}), number_prefix: fields[0].value, phase: fields[1].value, visibility: fields[2].value, offset, limit: 100};
  }
  function validQuery() {
    const valid = /^[0-9]{0,6}$/.test(fields[0].value);
    fields[0].setAttribute("aria-invalid", String(!valid));
    setText("number-error", valid ? "" : "ASCII 숫자 6자리까지 입력하세요.");
    const gameValid = fields[3].value === "" || /^[a-z0-9][a-z0-9_-]{0,63}$/.test(fields[3].value);
    fields[3].setAttribute("aria-invalid", String(!gameValid));
    setText("game-error", gameValid ? "" : "게임 ID는 소문자 영문·숫자로 시작하고 소문자 영문·숫자·_·- 64자까지 입력하세요.");
    return valid && gameValid;
  }
  function describeQuery(q) {
    return `게임 ${q.game_id || "전체"} · 룸 번호 ${q.number_prefix ? `${q.number_prefix}로 시작` : "전체"} · 상태 ${q.phase === "all" ? "전체" : q.phase === "WAITING" ? "대기" : "게임 중"} · 공개 여부 ${q.visibility === "all" ? "전체" : q.visibility === "public" ? "공개" : "비공개"}`;
  }
  function cancelCycle() {
    generation += 1;
    clearTimeout(timer); timer = null;
    if (active) active.controller.abort();
  }
  function age(data, receivedAt) { return Math.max(0, data.age_ms) + Math.max(0, performance.now() - receivedAt); }
  function freshness(data, receivedAt, failed = false) {
    const elapsed = age(data, receivedAt);
    return `${data.updated_at} · ${numberFormat.format(Math.floor(elapsed / 1000))}초 전 · ${failed || data.stale || elapsed >= 5000 ? "오래된 정보" : "최신 정보"}`;
  }
  function updateFreshness() {
    if (locked) return;
    if (statusData) {
      setText("status-freshness", `서버 요약: ${freshness(statusData.value, statusData.receivedAt, statusFailed)}`);
      $("status-freshness").classList.toggle("stale", statusFailed || statusData.value.stale || age(statusData.value, statusData.receivedAt) >= 5000);
    }
    if (roomsData) {
      setText("rooms-freshness", `룸 목록: ${freshness(roomsData.value, roomsData.receivedAt, roomsFailed)}`);
      $("rooms-freshness").classList.toggle("stale", roomsFailed || roomsData.value.stale || age(roomsData.value, roomsData.receivedAt) >= 5000);
      for (const {node, value} of rowFreshness) {
        node.textContent = freshness(value, roomsData.receivedAt, roomsFailed);
        node.classList.toggle("stale", roomsFailed || value.stale || age(value, roomsData.receivedAt) >= 5000);
      }
    }
  }
  function startDisplayTimer() {
    clearTimeout(displayTimer);
    updateFreshness();
    if (pageActive && !document.hidden && !locked) displayTimer = setTimeout(startDisplayTimer, 1000);
  }
  const integer = (value) => Number.isSafeInteger(value) && value >= 0;
  const timestamp = (value) => typeof value === "string" && Number.isFinite(Date.parse(value));
  function common(data) {
    return data && integer(data.age_ms) && integer(data.generation) && typeof data.stale === "boolean" && timestamp(data.updated_at);
  }
  function validateStatus(data) {
    return common(data) && typeof data.server_instance_id === "string" && ["RUNNING", "STOPPING"].includes(data.state) && typeof data.ready === "boolean" && timestamp(data.started_at)
      && ["active_connections", "room_member_count", "room_count", "waiting_room_count", "playing_room_count", "uptime_seconds", "heap_alloc_bytes", "goroutines"].every((key) => integer(data[key]))
      && ["cpu_percent", "rtt_ms", "client_display_delay_ms"].every((key) => data[key] === null);
  }
  function validateRooms(data) {
    return common(data) && ["total", "matched_total", "offset", "limit"].every((key) => integer(data[key])) && data.limit === 100 && data.matched_total <= data.total && Array.isArray(data.rooms) && data.rooms.length <= data.limit
      && data.rooms.every((room) => room && typeof room.game_id === "string" && /^[a-z0-9][a-z0-9_-]{0,63}$/.test(room.game_id) && ["room_id", "room_number", "host_id"].every((key) => typeof room[key] === "string") && ["public", "private"].includes(room.visibility) && ["WAITING", "PLAYING"].includes(room.phase) && integer(room.capacity) && integer(room.member_count) && (room.match_id === null || typeof room.match_id === "string") && timestamp(room.updated_at) && integer(room.age_ms) && typeof room.stale === "boolean");
  }
  async function readJSON(url, signal, validate) {
    const response = await fetch(url, {mode: "same-origin", credentials: "same-origin", cache: "no-store", signal});
    if (response.status === 401 || response.status === 403) {
      const error = new Error("Authentication required"); error.auth = true; throw error;
    }
    if (!response.ok) throw new Error("Request failed");
    const value = await response.json();
    if (!validate(value)) throw new Error("Invalid response");
    return {value, receivedAt: performance.now()};
  }
  function renderStatus(data) {
    const value = data.value;
    setText("server-status", `${value.state === "RUNNING" ? "서버 실행 중" : "서버 종료 진행 중"} · 신규 연결 ${value.ready ? "수용 가능" : "수용 중지"}`);
    [["connections", "active_connections"], ["members", "room_member_count"], ["room-count", "room_count"], ["waiting-count", "waiting_room_count"], ["playing-count", "playing_room_count"]].forEach(([id, key]) => setText(id, numberFormat.format(value[key])));
    setText("server-identity", `서버 ${value.server_instance_id} · 시작 ${value.started_at}`);
    const seconds = value.uptime_seconds;
    setText("runtime-status", `실행 시간 ${numberFormat.format(Math.floor(seconds / 3600))}시간 ${Math.floor(seconds % 3600 / 60)}분 ${seconds % 60}초 · heap ${(value.heap_alloc_bytes / 1048576).toFixed(1)} MiB · goroutine ${numberFormat.format(value.goroutines)} · CPU 미측정 · RTT 미측정 · 클라이언트 표시지연 미측정`);
  }
  function cell(row, text, className, heading = false) {
    const node = document.createElement(heading ? "th" : "td");
    if (heading) node.scope = "row";
    node.textContent = text;
    if (className) node.className = className;
    row.append(node);
    return node;
  }
  function renderRooms(data, requestedQuery) {
    roomsData = {...data, query: {...requestedQuery}};
    const value = data.value;
    const rows = [];
    rowFreshness.length = 0;
    for (const room of value.rooms) {
      const row = document.createElement("tr");
      cell(row, room.game_id);
      cell(row, room.room_number, "", true);
      cell(row, room.visibility === "public" ? "공개" : "비공개");
      const phase = cell(row, "");
      const badge = document.createElement("span"); badge.className = `badge${room.phase === "PLAYING" ? " playing" : ""}`; badge.textContent = room.phase === "WAITING" ? "대기" : "게임 중"; phase.append(badge);
      cell(row, `${room.member_count} / ${room.capacity}`, "numeric");
      cell(row, room.host_id); cell(row, room.room_id); cell(row, room.match_id === null ? "해당 없음" : room.match_id);
      const timeCell = cell(row, "");
      const time = document.createElement("span"); time.className = "row-freshness"; timeCell.append(time); rowFreshness.push({node: time, value: room});
      rows.push(row);
    }
    $("room-rows").replaceChildren(...rows);
    setText("rooms-count", `전체 ${numberFormat.format(value.total)}개 · 조건에 맞는 룸 ${numberFormat.format(value.matched_total)}개`);
    setText("rooms-message", value.total === 0 ? "현재 열린 룸이 없습니다." : value.matched_total === 0 ? "조건에 맞는 룸이 없습니다." : value.rooms.length === 0 ? "이 페이지에 룸이 없습니다. 이전 페이지를 확인하세요." : "");
    setText("page-info", value.rooms.length ? `${numberFormat.format(value.offset + 1)}–${numberFormat.format(value.offset + value.rooms.length)} / ${numberFormat.format(value.matched_total)}개` : `표시 0개 / ${numberFormat.format(value.matched_total)}개`);
    updateDisplayedQuery();
  }
  function updateDisplayedQuery() {
    if (!roomsData) return;
    const q = roomsData.query;
    const different = ["game_id", "number_prefix", "phase", "visibility", "offset"].some((key) => q[key] !== query()[key]);
    setText("displayed-query", `${different ? "이전 조회 결과를 표시합니다. " : "표시 중인 조회: "}${describeQuery(q)} · ${numberFormat.format(roomsData.value.offset)}부터`);
  }
  function updateControls() {
    refreshControl.disabled = !!active || locked;
    refreshControl.textContent = active && !locked ? "갱신 중" : "새로고침";
    const value = roomsData && roomsData.value;
    $("previous").disabled = locked || !!active || !value || value.offset === 0;
    $("next").disabled = locked || !!active || !value || value.offset + value.limit >= value.matched_total || value.offset + value.limit > 4096;
  }
  function lock() {
    locked = true; pendingRefresh = false; cancelCycle();
    clearTimeout(displayTimer); displayTimer = null;
    statusData = null; roomsData = null; rowFreshness.length = 0;
    $("room-rows").replaceChildren();
    ["connections", "members", "room-count", "waiting-count", "playing-count"].forEach((id) => setText(id, "—"));
    ["server-identity", "status-error", "rooms-error", "displayed-query", "number-error", "game-error"].forEach((id) => setText(id, ""));
    setText("server-status", "운영 인증이 필요합니다.");
    setText("status-freshness", "서버 요약 갱신 시각: —"); setText("rooms-freshness", "룸 목록 갱신 시각: —");
    setText("rooms-count", "전체 —개 · 조건에 맞는 룸 —개"); setText("rooms-message", "운영 인증이 필요합니다."); setText("page-info", "표시 범위 —");
    setText("runtime-status", "실행 시간 — · heap — MiB · goroutine — · CPU 미측정 · RTT 미측정 · 클라이언트 표시지연 미측정");
    setText("refresh-state", "인증 필요 · 자동 조회가 중지되었습니다.");
    fields.forEach((field) => { field.value = field.tagName === "SELECT" ? "all" : ""; field.disabled = true; });
    automaticControl.disabled = true; $("clear-filters").disabled = true; $("reauthenticate").hidden = false;
    document.body.dataset.state = "locked"; updateControls();
  }
  async function cycle() {
    if (locked || !pageActive || document.hidden) return;
    if (active) { pendingRefresh = true; return; }
    if (!validQuery()) { setText("refresh-state", "입력 조건을 확인하세요."); return; }
    clearTimeout(timer); timer = null;
    pendingRefresh = false;
    const epoch = ++generation;
    const controller = new AbortController();
    const requestedQuery = query();
    active = {controller, epoch};
    document.body.dataset.state = "loading";
    setText("refresh-state", "서버와 룸 목록을 조회합니다."); updateControls();
    const timeout = setTimeout(() => controller.abort(), 5000);
    try {
      const results = await Promise.allSettled([
        readJSON("/admin/api/status", controller.signal, validateStatus),
        readJSON(`/admin/api/rooms?${new URLSearchParams(requestedQuery)}`, controller.signal, validateRooms)
      ]);
      if (epoch !== generation || locked) return;
      if (results.some((result) => result.status === "rejected" && result.reason.auth)) { lock(); return; }
      const [server, rooms] = results;
      statusFailed = server.status === "rejected";
      roomsFailed = rooms.status === "rejected";
      if (!statusFailed) { statusData = server.value; renderStatus(statusData); setText("status-error", ""); }
      else {
        setText("status-error", statusData ? "서버 상태 갱신 실패. 마지막 서버 요약과 시각을 유지합니다." : "서버 상태를 불러오지 못했습니다. 새로고침으로 다시 조회하세요.");
        if (!statusData) setText("server-status", "서버 상태 확인 불가");
      }
      if (!roomsFailed) { renderRooms(rooms.value, requestedQuery); setText("rooms-error", ""); }
      else {
        setText("rooms-error", roomsData ? "룸 목록 갱신 실패. 마지막 조회 결과와 조건을 유지합니다." : "룸 목록을 불러오지 못했습니다. 새로고침으로 다시 조회하세요.");
        if (!roomsData) setText("rooms-message", "룸 목록을 불러오지 못했습니다.");
      }
      updateDisplayedQuery(); updateFreshness();
      document.body.dataset.state = statusFailed || roomsFailed ? "error" : "ready";
      setText("refresh-state", statusFailed || roomsFailed ? "일부 조회에 실패했습니다." : "조회가 완료되었습니다.");
    } finally {
      clearTimeout(timeout);
      if (epoch !== generation && !locked) {
        document.body.dataset.state = statusFailed || roomsFailed ? "error" : statusData || roomsData ? "ready" : "idle";
        setText("refresh-state", statusData || roomsData ? "조회가 중단되었습니다. 마지막 자료를 유지합니다." : "조회가 중단되었습니다. 새로고침으로 다시 조회하세요.");
      }
      active = null; updateControls();
      if (pendingRefresh && pageActive && !document.hidden && !locked) { pendingRefresh = false; void cycle(); }
      else if (automatic && pageActive && !document.hidden && !locked) timer = setTimeout(cycle, 5000);
    }
  }
  function filtersChanged() {
    offset = 0; cancelCycle(); pendingRefresh = false;
    updateDisplayedQuery();
    if (!validQuery() || locked || document.hidden) return;
    pendingRefresh = true;
    if (!active) void cycle();
  }
  fields[0].addEventListener("input", filtersChanged);
  fields[3].addEventListener("input", filtersChanged);
  fields.slice(1, 3).forEach((field) => field.addEventListener("change", filtersChanged));
  refreshControl.addEventListener("click", () => { if (!active && !locked) void cycle(); });
  automaticControl.addEventListener("change", () => {
    automatic = automaticControl.checked;
    clearTimeout(timer); timer = null;
    if (automatic) { visibleResumed = false; resumeAutomaticCycle(); }
  });
  $("clear-filters").addEventListener("click", () => {
    fields[0].value = ""; fields[1].value = "all"; fields[2].value = "all"; fields[3].value = ""; filtersChanged();
  });
  function page(direction) {
    if (!roomsData || active || locked) return;
    const value = roomsData.value, previousQuery = roomsData.query;
    const target = value.offset + direction * 100;
    if (target < 0 || target > 4096 || direction > 0 && target >= value.matched_total) return;
    fields[0].value = previousQuery.number_prefix; fields[1].value = previousQuery.phase; fields[2].value = previousQuery.visibility; fields[3].value = previousQuery.game_id || "";
    offset = target; void cycle();
  }
  $("previous").addEventListener("click", () => page(-1));
  $("next").addEventListener("click", () => page(1));
  $("reauthenticate").addEventListener("click", () => location.reload());
  function resumeAutomaticCycle() {
    if (!automatic || locked || !pageActive || document.hidden || visibleResumed) return;
    visibleResumed = true;
    if (!active || active.epoch !== generation) void cycle();
  }
  document.addEventListener("visibilitychange", () => {
    if (document.hidden) {
      visibleResumed = false;
      cancelCycle(); pendingRefresh = false; clearTimeout(displayTimer); displayTimer = null;
    } else {
      startDisplayTimer();
      if (automatic) resumeAutomaticCycle();
      else pendingRefresh = false;
    }
  });
  window.addEventListener("pagehide", () => { pageActive = false; visibleResumed = false; cancelCycle(); pendingRefresh = false; clearTimeout(displayTimer); displayTimer = null; });
  window.addEventListener("pageshow", (event) => {
    if (!event.persisted) return;
    pageActive = true;
    startDisplayTimer();
    resumeAutomaticCycle();
  });
  startDisplayTimer(); void cycle();
})();
