var GoChartManager = (function () {
  var ws = null;
  var wsUrl = 'ws://localhost:9750/ws/tasks';
  var reconnectTimer = null;
  var reconnectAttempts = 0;
  var maxReconnectAttempts = 10;
  var reconnectDelay = 3000;
  var autoSendEnabled = false;
  var autoSendOnWsUpdate = true;
  var connected = false;

  function connect(url) {
    if (url) wsUrl = url;
    if (ws && (ws.readyState === WebSocket.CONNECTING || ws.readyState === WebSocket.OPEN)) {
      addLog('WebSocket 已连接或正在连接');
      return;
    }

    try {
      ws = new WebSocket(wsUrl);
    } catch (e) {
      addLog('WebSocket 创建失败: ' + e.message);
      return;
    }

    ws.onopen = function () {
      connected = true;
      reconnectAttempts = 0;
      addLog('WebSocket 已连接: ' + wsUrl);
      updateWsStatus('connected');
    };

    ws.onclose = function (e) {
      connected = false;
      updateWsStatus('disconnected');
      if (e.code !== 1000) {
        addLog('WebSocket 断开 (code: ' + e.code + '), 将尝试重连...');
        scheduleReconnect();
      }
    };

    ws.onerror = function (e) {
      addLog('WebSocket 错误');
    };

    ws.onmessage = function (event) {
      handleMessage(event.data);
    };
  }

  function disconnect() {
    if (reconnectTimer) {
      clearTimeout(reconnectTimer);
      reconnectTimer = null;
    }
    reconnectAttempts = maxReconnectAttempts;
    if (ws) {
      ws.close(1000, '用户主动断开');
      ws = null;
    }
    connected = false;
    updateWsStatus('disconnected');
    addLog('WebSocket 已断开');
  }

  function scheduleReconnect() {
    if (reconnectAttempts >= maxReconnectAttempts) {
      addLog('WebSocket 重连次数已达上限，请手动重连');
      return;
    }
    reconnectAttempts++;
    var delay = reconnectDelay * Math.min(reconnectAttempts, 5);
    addLog(reconnectDelay > 0 ? '将在 ' + (delay / 1000) + 's 后重连 (' + reconnectAttempts + '/' + maxReconnectAttempts + ')' : '');
    reconnectTimer = setTimeout(function () {
      connect();
    }, delay);
  }

  function handleMessage(raw) {
    var msg;
    try {
      msg = JSON.parse(raw);
    } catch (e) {
      addLog('收到非 JSON 消息: ' + raw);
      return;
    }

    switch (msg.type) {
      case 'task_update':
        handleTaskUpdate(msg);
        break;
      case 'ping':
        if (ws && ws.readyState === WebSocket.OPEN) {
          ws.send(JSON.stringify({ type: 'pong' }));
        }
        break;
      default:
        addLog('收到未知消息类型: ' + msg.type);
    }
  }

  function handleTaskUpdate(msg) {
    addLog('收到任务更新 (' + (msg.tasks ? msg.tasks.length : 0) + ' 项)');

    if (typeof TodoManager === 'undefined') {
      addLog('TodoManager 未就绪，忽略任务更新');
      return;
    }

    if (msg.title) {
      var titleInput = document.getElementById('todo-title');
      if (titleInput) titleInput.value = msg.title;
    }
    if (msg.date) {
      var dateInput = document.getElementById('todo-date');
      if (dateInput) dateInput.value = msg.date;
    }

    if (msg.tasks) {
      TodoManager.setTasks(msg.tasks);
    }

    if (autoSendOnWsUpdate && autoSendEnabled) {
      addLog('自动生成并发送任务图片...');
      setTimeout(function () {
        TodoManager.renderAndSend();
      }, 200);
    }
  }

  function setAutoSend(enabled) {
    autoSendEnabled = enabled;
    var btn = document.getElementById('auto-send-toggle');
    if (btn) {
      btn.textContent = enabled ? '自动发送: 开' : '自动发送: 关';
      btn.classList.toggle('primary', enabled);
      btn.classList.toggle('secondary', !enabled);
    }
    addLog('自动发送已' + (enabled ? '开启' : '关闭'));
  }

  function toggleAutoSend() {
    setAutoSend(!autoSendEnabled);
  }

  function isConnected() {
    return connected;
  }

  function isAutoSendEnabled() {
    return autoSendEnabled;
  }

  function sendAudioAttachment(att) {
    if (!ws || ws.readyState !== WebSocket.OPEN) {
      addLog('WebSocket 未连接，无法发送音频');
      return;
    }
    var msg = JSON.stringify({
      type: 'audio',
      attachment: att,
      timestamp: Date.now()
    });
    ws.send(msg);
    addLog('音频附件已通过 WebSocket 发送');
  }

  function updateWsStatus(status) {
    var el = document.getElementById('ws-status');
    if (!el) return;
    switch (status) {
      case 'connected':
        el.textContent = '已连接';
        el.style.color = '#198754';
        break;
      case 'connecting':
        el.textContent = '连接中...';
        el.style.color = '#fd7e14';
        break;
      case 'disconnected':
        el.textContent = '未连接';
        el.style.color = '#dc3545';
        break;
    }
  }

  function init() {
    var urlInput = document.getElementById('ws-url');
    if (urlInput && urlInput.value) {
      wsUrl = urlInput.value;
    }
  }

  return {
    connect: connect,
    disconnect: disconnect,
    setAutoSend: setAutoSend,
    toggleAutoSend: toggleAutoSend,
    isConnected: isConnected,
    isAutoSendEnabled: isAutoSendEnabled,
    sendAudioAttachment: sendAudioAttachment,
    init: init
  };
})();
