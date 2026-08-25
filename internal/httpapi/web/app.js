const state={csrf:"",jobId:sessionStorage.getItem("btc_job_id")||"",timer:null};
const $=id=>document.getElementById(id);
const stages={waiting:["等待中","任务已建立，正在准备解析器。"],preflight:["预检","正在检查页数、安全限制与文本层。"],extracting:["提取内容","正在读取文字、版面与插图。"],rebuilding:["重建结构","正在合并段落并整理目录。"],packaging:["生成 EPUB","正在写入章节、封面与样式。"],validating:["规范校验","正在运行 EPUBCheck。"],completed:["转换完成","EPUB 已通过校验，可以下载。"],failed:["转换失败","转换没有生成可下载文件。"],canceled:["已取消","任务已取消，临时文件已清理。"]};

async function api(url,options={}){const response=await fetch(url,{credentials:"same-origin",...options,headers:{...(options.body instanceof FormData?{}:{"Content-Type":"application/json"}),...(state.csrf?{"X-CSRF-Token":state.csrf}:{}),...options.headers}});if(response.status===204)return null;const body=await response.json().catch(()=>({error:{message:"服务器返回了无法识别的响应。"}}));if(!response.ok)throw new Error(body.error?.message||"请求失败，请稍后重试。");return body}
function showMessage(message){$("message").textContent=message;$("message").hidden=false;clearTimeout(showMessage.timer);showMessage.timer=setTimeout(()=>$("message").hidden=true,6000)}
function showWorkspace(session){state.csrf=session.csrf_token;$("loginPanel").hidden=true;$("workspace").hidden=false;$("logoutButton").hidden=false;if(state.jobId)pollJob()}
async function restore(){try{showWorkspace(await api("/api/v1/session"))}catch{state.jobId="";sessionStorage.removeItem("btc_job_id");$("loginPanel").hidden=false}}

$("loginForm").addEventListener("submit",async event=>{event.preventDefault();try{const session=await api("/api/v1/auth/login",{method:"POST",body:JSON.stringify({username:$("username").value,password:$("password").value})});$("password").value="";showWorkspace(session)}catch(error){showMessage(error.message)}});
$("logoutButton").addEventListener("click",async()=>{try{await api("/api/v1/auth/logout",{method:"POST",body:"{}"})}catch{}location.reload()});

const fileInput=$("fileInput"),dropzone=$("dropzone");
function selectFile(file){if(!file)return;if(file.size>100*1024*1024){showMessage("PDF 不能超过 100 MiB。");fileInput.value="";return}if(!file.name.toLowerCase().endsWith(".pdf")){showMessage("请选择 PDF 文件。");fileInput.value="";return}$("fileLabel").textContent=file.name;$("submitButton").disabled=false;$("clearButton").hidden=false}
fileInput.addEventListener("change",()=>selectFile(fileInput.files[0]));
["dragenter","dragover"].forEach(name=>dropzone.addEventListener(name,event=>{event.preventDefault();dropzone.classList.add("is-dragging")}));
["dragleave","drop"].forEach(name=>dropzone.addEventListener(name,event=>{event.preventDefault();dropzone.classList.remove("is-dragging")}));
dropzone.addEventListener("drop",event=>{if(event.dataTransfer.files[0]){const transfer=new DataTransfer();transfer.items.add(event.dataTransfer.files[0]);fileInput.files=transfer.files;selectFile(fileInput.files[0])}});
$("clearButton").addEventListener("click",()=>{fileInput.value="";$("fileLabel").textContent="拖入 PDF，或点击选择";$("submitButton").disabled=true;$("clearButton").hidden=true});

$("uploadForm").addEventListener("submit",async event=>{event.preventDefault();if(!fileInput.files[0])return;const form=new FormData();form.append("file",fileInput.files[0]);$("submitButton").disabled=true;try{const job=await api("/api/v1/jobs",{method:"POST",body:form});state.jobId=job.id;sessionStorage.setItem("btc_job_id",job.id);renderJob(job);pollJob()}catch(error){showMessage(error.message);$("submitButton").disabled=false}});
$("cancelButton").addEventListener("click",async()=>{if(!state.jobId)return;$("cancelButton").disabled=true;try{await api(`/api/v1/jobs/${state.jobId}/cancel`,{method:"POST",body:"{}"});await pollJob()}catch(error){showMessage(error.message)}finally{$("cancelButton").disabled=false}});

function renderJob(job){$("jobPanel").hidden=false;$("jobPanel").className=`job-panel is-${job.status}`;const stage=stages[job.stage]||[job.stage,"正在处理。"];
$("jobTitle").textContent=stage[0];$("statusPill").textContent=stage[0];let detail=stage[1];if(job.total_pages){detail+=` 已处理 ${job.processed_pages||0} / ${job.total_pages} 页。`}if(job.failure)detail=job.failure.page?`第 ${job.failure.page} 页：${job.failure.message}`:job.failure.message;$("jobDetail").textContent=detail;
const progress=job.total_pages?Math.min(92,Math.max(3,((job.processed_pages||0)/job.total_pages)*82)):3;$("progressBar").style.width=job.status==="succeeded"?"100%":`${progress}%`;
$("warningList").replaceChildren(...(job.warnings||[]).map(w=>{const item=document.createElement("div");item.className="warning";item.textContent=(w.page?`第 ${w.page} 页：`:"")+w.message;return item}));
const terminal=["succeeded","failed","canceled"].includes(job.status);$("cancelButton").hidden=terminal;$("downloadButton").hidden=job.status!=="succeeded";if(job.status==="succeeded")$("downloadButton").href=`/api/v1/jobs/${job.id}/download`;$("jobPanel").scrollIntoView({behavior:"smooth",block:"nearest"});return terminal}
async function pollJob(){clearTimeout(state.timer);if(!state.jobId)return;try{const job=await api(`/api/v1/jobs/${state.jobId}`);if(!renderJob(job))state.timer=setTimeout(pollJob,2000)}catch(error){sessionStorage.removeItem("btc_job_id");state.jobId="";showMessage(error.message)}}
restore();
