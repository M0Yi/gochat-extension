var AudioManager = (function () {
  var mediaRecorder = null;
  var audioChunks = [];
  var recTimerInterval = null;
  var recSeconds = 0;
  var isRecording = false;

  function toggleRecording() {
    if (isRecording) {
      stopRecording();
      return;
    }
    navigator.mediaDevices.getUserMedia({ audio: true }).then(function (stream) {
      startRecording(stream);
    }).catch(function (e) {
      alert('麦克风授权失败: ' + (e.message || e));
    });
  }

  function startRecording(stream) {
    audioChunks = [];
    isRecording = true;

    var options = {};
    if (typeof MediaRecorder !== 'undefined') {
      if (MediaRecorder.isTypeSupported('audio/webm;codecs=opus')) {
        options.mimeType = 'audio/webm;codecs=opus';
      } else if (MediaRecorder.isTypeSupported('audio/ogg;codecs=opus')) {
        options.mimeType = 'audio/ogg;codecs=opus';
      }
    }

    mediaRecorder = new MediaRecorder(stream, options);

    mediaRecorder.ondataavailable = function (e) {
      if (e.data.size > 0) audioChunks.push(e.data);
    };

    mediaRecorder.onstop = function () {
      stream.getTracks().forEach(function (t) { t.stop(); });
      var blob = new Blob(audioChunks, { type: mediaRecorder.mimeType || 'audio/webm' });
      var ext = (mediaRecorder.mimeType && mediaRecorder.mimeType.indexOf('ogg') !== -1) ? '.ogg' : '.webm';
      showRecordingResult(blob, ext);
      clearInterval(recTimerInterval);
      isRecording = false;
      updateRecordUI();
    };

    mediaRecorder.start(250);

    var btn = document.getElementById('record-btn');
    if (btn) {
      btn.classList.add('recording');
      btn.innerHTML = '&#9679; <span id="rec-timer">0s</span>';
    }

    recSeconds = 0;
    recTimerInterval = setInterval(function () {
      recSeconds++;
      var el = document.getElementById('rec-timer');
      if (el) el.textContent = recSeconds + 's';
    }, 1000);
  }

  function stopRecording() {
    if (mediaRecorder && mediaRecorder.state === 'recording') {
      mediaRecorder.stop();
    }
    updateRecordUI();
  }

  function updateRecordUI() {
    var btn = document.getElementById('record-btn');
    if (!btn) return;
    btn.classList.remove('recording');
    if (!isRecording) {
      btn.innerHTML = '&#127908; 录音';
    }
  }

  function showRecordingResult(blob, ext) {
    var resultEl = document.getElementById('audio-result');
    if (!resultEl) return;

    var url = URL.createObjectURL(blob);
    resultEl.style.display = 'block';
    resultEl.innerHTML = '';

    var audio = document.createElement('audio');
    audio.controls = true;
    audio.src = url;
    audio.className = 'audio-result-player';
    resultEl.appendChild(audio);

    var info = document.createElement('span');
    info.className = 'audio-result-info';
    info.textContent = recSeconds + 's';
    resultEl.appendChild(info);

    var sendBtn = document.createElement('button');
    sendBtn.className = 'primary';
    sendBtn.textContent = '上传发送';
    sendBtn.onclick = function () { uploadAndSend(blob, ext); };
    resultEl.appendChild(sendBtn);

    var discardBtn = document.createElement('button');
    discardBtn.className = 'secondary';
    discardBtn.textContent = '丢弃';
    discardBtn.onclick = function () {
      URL.revokeObjectURL(url);
      resultEl.style.display = 'none';
      resultEl.innerHTML = '';
    };
    resultEl.appendChild(discardBtn);
  }

  function uploadAndSend(blob, ext) {
    var wsUrlInput = document.getElementById('ws-url');
    var baseUrl = '';
    if (wsUrlInput && wsUrlInput.value) {
      var wsUrl = wsUrlInput.value;
      baseUrl = wsUrl.replace(/\/ws\/tasks$/, '').replace(/^ws/, 'http');
    }
    if (!baseUrl) {
      addLog('请先连接 WebSocket 以确定服务器地址');
      return;
    }

    var fileName = 'recording_' + Date.now() + ext;
    var formData = new FormData();
    formData.append('file', blob, fileName);

    addLog('正在上传录音...');

    fetch(baseUrl + '/api/upload', {
      method: 'POST',
      body: formData
    }).then(function (res) {
      if (!res.ok) throw new Error('upload failed: ' + res.status);
      return res.json();
    }).then(function (att) {
      addLog('录音已上传: ' + (att.name || att.url));

      if (typeof GoChartManager !== 'undefined' && GoChartManager.isConnected()) {
        GoChartManager.sendAudioAttachment(att);
      } else {
        addLog('WebSocket 未连接，录音已上传但未发送到设备');
      }

      var resultEl = document.getElementById('audio-result');
      if (resultEl) {
        resultEl.style.display = 'none';
        resultEl.innerHTML = '';
      }
    }).catch(function (e) {
      addLog('上传录音失败: ' + e.message);
    });
  }

  return {
    toggleRecording: toggleRecording,
    isRecording: function () { return isRecording; }
  };
})();
