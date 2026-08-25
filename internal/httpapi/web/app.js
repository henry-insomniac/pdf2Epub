const state = {
  csrf: "",
  jobId: sessionStorage.getItem("btc_job_id") || "",
  timer: null,
  uploadRequest: null,
  uploading: false,
};

const $ = id => document.getElementById(id);
const stages = {
  waiting: ["等待中", "任务已建立，正在准备解析器。"],
  preflight: ["预检", "正在检查页数、安全限制与适合版式。"],
  extracting: ["提取内容", "正在读取文字与插图，或渲染固定版式页面。"],
  rebuilding: ["重建结构", "正在组织段落、目录或双页顺序。"],
  packaging: ["生成 EPUB", "正在写入章节、页面、封面与样式。"],
  validating: ["规范校验", "正在运行 EPUBCheck。"],
  completed: ["转换完成", "EPUB 已通过校验，可以下载。"],
  failed: ["转换失败", "转换没有生成可下载文件。"],
  canceled: ["已取消", "任务已取消，临时文件已清理。"],
};

class UploadCanceledError extends Error {}

async function api(url, options = {}) {
  const response = await fetch(url, {
    credentials: "same-origin",
    ...options,
    headers: {
      ...(options.body instanceof FormData ? {} : {"Content-Type": "application/json"}),
      ...(state.csrf ? {"X-CSRF-Token": state.csrf} : {}),
      ...options.headers,
    },
  });
  if (response.status === 204) return null;
  const body = await response.json().catch(() => ({error: {message: "服务器返回了无法识别的响应。"}}));
  if (!response.ok) throw new Error(body.error?.message || "请求失败，请稍后重试。");
  return body;
}

function uploadJob(form, onProgress) {
  return new Promise((resolve, reject) => {
    const request = new XMLHttpRequest();
    state.uploadRequest = request;
    request.open("POST", "/api/v1/jobs");
    request.withCredentials = true;
    if (state.csrf) request.setRequestHeader("X-CSRF-Token", state.csrf);
    request.upload.addEventListener("progress", event => {
      onProgress(event.loaded, event.lengthComputable ? event.total : 0);
    });
    request.addEventListener("load", () => {
      let body;
      try {
        body = JSON.parse(request.responseText);
      } catch {
        body = {error: {message: "服务器返回了无法识别的响应。"}};
      }
      if (request.status >= 200 && request.status < 300) {
        resolve(body);
        return;
      }
      reject(new Error(body.error?.message || "上传失败，请稍后重试。"));
    });
    request.addEventListener("error", () => reject(new Error("上传失败，请检查网络后重试。")));
    request.addEventListener("abort", () => reject(new UploadCanceledError("上传已取消。")));
    request.addEventListener("loadend", () => {
      if (state.uploadRequest === request) state.uploadRequest = null;
    });
    request.send(form);
  });
}

function showMessage(message) {
  $("message").textContent = message;
  $("message").hidden = false;
  clearTimeout(showMessage.timer);
  showMessage.timer = setTimeout(() => $("message").hidden = true, 6000);
}

function showWorkspace(session) {
  state.csrf = session.csrf_token;
  $("loginPanel").hidden = true;
  $("workspace").hidden = false;
  $("logoutButton").hidden = false;
  if (state.jobId) pollJob();
}

async function restore() {
  try {
    showWorkspace(await api("/api/v1/session"));
  } catch {
    state.jobId = "";
    sessionStorage.removeItem("btc_job_id");
    $("loginPanel").hidden = false;
  }
}

$("loginForm").addEventListener("submit", async event => {
  event.preventDefault();
  try {
    const session = await api("/api/v1/auth/login", {
      method: "POST",
      body: JSON.stringify({username: $("username").value, password: $("password").value}),
    });
    $("password").value = "";
    showWorkspace(session);
  } catch (error) {
    showMessage(error.message);
  }
});

$("logoutButton").addEventListener("click", async () => {
  try {
    await api("/api/v1/auth/logout", {method: "POST", body: "{}"});
  } catch {}
  location.reload();
});

const fileInput = $("fileInput");
const dropzone = $("dropzone");
const modeInputs = [...document.querySelectorAll('input[name="mode"]')];

function setSourceControlsDisabled(disabled) {
  fileInput.disabled = disabled;
  modeInputs.forEach(input => { input.disabled = disabled; });
  $("clearButton").disabled = disabled;
  dropzone.classList.toggle("is-disabled", disabled);
}

function setUploadPending(pending) {
  state.uploading = pending;
  $("uploadForm").setAttribute("aria-busy", String(pending));
  setSourceControlsDisabled(pending);
  $("submitButton").disabled = pending || !fileInput.files[0];
  if (!pending) $("submitButton").textContent = "开始转换";
}

function selectFile(file) {
  if (!file) return;
  if (file.size > 100 * 1024 * 1024) {
    showMessage("PDF 不能超过 100 MiB。");
    fileInput.value = "";
    return;
  }
  if (!file.name.toLowerCase().endsWith(".pdf")) {
    showMessage("请选择 PDF 文件。");
    fileInput.value = "";
    return;
  }
  $("fileLabel").textContent = file.name;
  $("submitButton").disabled = false;
  $("clearButton").hidden = false;
}

fileInput.addEventListener("change", () => selectFile(fileInput.files[0]));
["dragenter", "dragover"].forEach(name => dropzone.addEventListener(name, event => {
  event.preventDefault();
  if (!state.uploading) dropzone.classList.add("is-dragging");
}));
["dragleave", "drop"].forEach(name => dropzone.addEventListener(name, event => {
  event.preventDefault();
  dropzone.classList.remove("is-dragging");
}));
dropzone.addEventListener("drop", event => {
  if (state.uploading || !event.dataTransfer.files[0]) return;
  const transfer = new DataTransfer();
  transfer.items.add(event.dataTransfer.files[0]);
  fileInput.files = transfer.files;
  selectFile(fileInput.files[0]);
});

$("clearButton").addEventListener("click", () => {
  if (state.uploading) return;
  fileInput.value = "";
  $("fileLabel").textContent = "拖入 PDF，或点击选择";
  $("submitButton").disabled = true;
  $("clearButton").hidden = true;
});

function formatBytes(bytes) {
  if (!Number.isFinite(bytes) || bytes <= 0) return "0 MiB";
  return `${(bytes / 1024 / 1024).toFixed(1)} MiB`;
}

function renderUpload(loaded, total) {
  const measurable = total > 0;
  const percent = measurable ? Math.min(100, Math.round((loaded / total) * 100)) : 0;
  const complete = measurable && percent >= 100;
  const progressTrack = $("progressBar").parentElement;

  $("jobPanel").hidden = false;
  $("jobPanel").className = "job-panel is-uploading";
  $("jobTitle").textContent = complete ? "正在创建转换任务" : "正在上传 PDF";
  $("statusPill").textContent = complete ? "已上传" : "上传中";
  $("jobDetail").textContent = complete
    ? "PDF 已上传，服务器正在检查文件并创建转换任务。"
    : measurable
      ? `已上传 ${formatBytes(loaded)} / ${formatBytes(total)}（${percent}%）。请不要关闭页面。`
      : `正在上传 ${formatBytes(loaded)}，请不要关闭页面。`;
  $("progressBar").style.width = measurable ? `${Math.max(2, percent)}%` : "8%";
  progressTrack.setAttribute("aria-valuenow", measurable ? String(percent) : "0");
  progressTrack.setAttribute("aria-valuetext", complete ? "上传完成，正在创建任务" : `PDF 上传 ${percent}%`);
  $("warningList").replaceChildren();
  $("cancelButton").textContent = "取消上传";
  $("cancelButton").hidden = false;
  $("cancelButton").disabled = false;
  $("downloadButton").hidden = true;
  $("submitButton").textContent = complete ? "正在创建任务…" : `正在上传 ${percent}%`;
}

function renderUploadCanceled() {
  $("jobPanel").hidden = false;
  $("jobPanel").className = "job-panel is-canceled";
  $("jobTitle").textContent = "上传已取消";
  $("statusPill").textContent = "已取消";
  $("jobDetail").textContent = "文件上传已停止，没有创建转换任务。你可以重新开始。";
  $("progressBar").style.width = "0%";
  $("cancelButton").hidden = true;
  $("downloadButton").hidden = true;
}

function renderUploadFailure(message) {
  $("jobPanel").hidden = false;
  $("jobPanel").className = "job-panel is-failed";
  $("jobTitle").textContent = "上传失败";
  $("statusPill").textContent = "上传失败";
  $("jobDetail").textContent = message;
  $("cancelButton").hidden = true;
  $("downloadButton").hidden = true;
}

$("uploadForm").addEventListener("submit", async event => {
  event.preventDefault();
  const file = fileInput.files[0];
  if (!file || state.uploading) return;

  const form = new FormData();
  form.append("file", file);
  form.append("mode", document.querySelector('input[name="mode"]:checked')?.value || "auto");
  state.jobId = "";
  sessionStorage.removeItem("btc_job_id");
  setUploadPending(true);
  renderUpload(0, file.size);
  $("jobPanel").scrollIntoView({behavior: "smooth", block: "nearest"});

  try {
    const job = await uploadJob(form, renderUpload);
    state.uploading = false;
    $("uploadForm").setAttribute("aria-busy", "false");
    setSourceControlsDisabled(false);
    $("submitButton").disabled = true;
    $("submitButton").textContent = "转换进行中";
    state.jobId = job.id;
    sessionStorage.setItem("btc_job_id", job.id);
    renderJob(job);
    pollJob();
  } catch (error) {
    setUploadPending(false);
    if (error instanceof UploadCanceledError) {
      renderUploadCanceled();
      return;
    }
    renderUploadFailure(error.message);
    showMessage(error.message);
  }
});

$("cancelButton").addEventListener("click", async () => {
  if (state.uploadRequest) {
    $("cancelButton").disabled = true;
    state.uploadRequest.abort();
    return;
  }
  if (!state.jobId) return;
  $("cancelButton").disabled = true;
  try {
    await api(`/api/v1/jobs/${state.jobId}/cancel`, {method: "POST", body: "{}"});
    await pollJob();
  } catch (error) {
    showMessage(error.message);
  } finally {
    $("cancelButton").disabled = false;
  }
});

function renderJob(job) {
  $("jobPanel").hidden = false;
  $("jobPanel").className = `job-panel is-${job.status}`;
  const stage = stages[job.stage] || [job.stage, "正在处理。"];
  $("jobTitle").textContent = stage[0];
  $("statusPill").textContent = stage[0];
  let detail = stage[1];
  if (job.total_pages) detail += ` 已处理 ${job.processed_pages || 0} / ${job.total_pages} 页。`;
  if (job.failure) detail = job.failure.page ? `第 ${job.failure.page} 页：${job.failure.message}` : job.failure.message;
  $("jobDetail").textContent = detail;
  const progress = job.total_pages
    ? Math.min(92, Math.max(3, ((job.processed_pages || 0) / job.total_pages) * 82))
    : 3;
  const visibleProgress = job.status === "succeeded" ? 100 : Math.round(progress);
  $("progressBar").style.width = `${visibleProgress}%`;
  const progressTrack = $("progressBar").parentElement;
  progressTrack.setAttribute("aria-valuenow", String(visibleProgress));
  progressTrack.setAttribute("aria-valuetext", job.status === "succeeded" ? "转换完成" : `${stage[0]}，${visibleProgress}%`);
  $("warningList").replaceChildren(...(job.warnings || []).map(warning => {
    const item = document.createElement("div");
    item.className = "warning";
    item.textContent = (warning.page ? `第 ${warning.page} 页：` : "") + warning.message;
    return item;
  }));
  const terminal = ["succeeded", "failed", "canceled"].includes(job.status);
  $("cancelButton").textContent = "取消转换";
  $("cancelButton").hidden = terminal;
  $("downloadButton").hidden = job.status !== "succeeded";
  if (job.status === "succeeded") $("downloadButton").href = `/api/v1/jobs/${job.id}/download`;
  if (terminal) setUploadPending(false);
  $("jobPanel").scrollIntoView({behavior: "smooth", block: "nearest"});
  return terminal;
}

async function pollJob() {
  clearTimeout(state.timer);
  if (!state.jobId) return;
  try {
    const job = await api(`/api/v1/jobs/${state.jobId}`);
    if (!renderJob(job)) state.timer = setTimeout(pollJob, 2000);
  } catch (error) {
    sessionStorage.removeItem("btc_job_id");
    state.jobId = "";
    showMessage(error.message);
  }
}

restore();
