var TodoManager = (function () {
  var STORAGE_KEY = 'epd_todo_tasks';

  var defaultTasks = [
    { text: '\u5B8C\u6210\u9879\u76EE\u62A5\u544A', done: false },
    { text: '\u56DE\u590D\u90AE\u4EF6\u548C\u6D88\u606F', done: true },
    { text: '\u51C6\u5907\u660E\u5929\u7684\u4F1A\u8BAE\u8D44\u6599', done: false },
    { text: '\u53BB\u8D85\u5E02\u91C7\u8D2D\u751F\u9C9C\u98DF\u6750', done: false },
    { text: '\u8FD0\u52A830\u5206\u949F', done: false },
    { text: '\u9605\u8BFB\u4E00\u7AE0\u4E66', done: true },
    { text: '\u6574\u7406\u5DE5\u4F5C\u53F0\u9762', done: false },
    { text: '\u5B66\u4E60\u65B0\u6280\u672F\u6587\u6863', done: false }
  ];

  var tasks = loadTasks();

  function loadTasks() {
    try {
      var data = localStorage.getItem(STORAGE_KEY);
      if (data) return JSON.parse(data);
    } catch (e) { }
    return defaultTasks.map(function (t) { return { text: t.text, done: t.done }; });
  }

  function saveTasks() {
    try {
      localStorage.setItem(STORAGE_KEY, JSON.stringify(tasks));
    } catch (e) { }
  }

  function addTask() {
    var input = document.getElementById('todo-input');
    var text = input.value.trim();
    if (!text) return;
    tasks.push({ text: text, done: false });
    input.value = '';
    saveTasks();
    renderList();
    triggerAutoSend();
  }

  function toggleTask(index) {
    tasks[index].done = !tasks[index].done;
    saveTasks();
    renderList();
    triggerAutoSend();
  }

  function removeTask(index) {
    tasks.splice(index, 1);
    saveTasks();
    renderList();
    triggerAutoSend();
  }

  function moveTask(index, direction) {
    var newIndex = index + direction;
    if (newIndex < 0 || newIndex >= tasks.length) return;
    var temp = tasks[index];
    tasks[index] = tasks[newIndex];
    tasks[newIndex] = temp;
    saveTasks();
    renderList();
    triggerAutoSend();
  }

  function clearAll() {
    if (tasks.length === 0) return;
    if (!confirm('确认清空所有任务?')) return;
    tasks = [];
    saveTasks();
    renderList();
    triggerAutoSend();
  }

  function renderList() {
    var list = document.getElementById('todo-list');
    if (!list) return;
    list.innerHTML = '';
    if (tasks.length === 0) {
      list.innerHTML = '<div class="todo-empty">暂无任务，请添加</div>';
      return;
    }
    tasks.forEach(function (task, i) {
      var item = document.createElement('div');
      item.className = 'todo-item' + (task.done ? ' done' : '');

      var checkbox = document.createElement('input');
      checkbox.type = 'checkbox';
      checkbox.checked = task.done;
      checkbox.className = 'todo-checkbox';
      checkbox.addEventListener('change', function () { toggleTask(i); });

      var label = document.createElement('span');
      label.className = 'todo-text';
      label.textContent = task.text;

      var actions = document.createElement('span');
      actions.className = 'todo-actions';

      var upBtn = document.createElement('button');
      upBtn.textContent = '\u2191';
      upBtn.className = 'todo-btn-sm';
      upBtn.title = '\u4E0A\u79FB';
      upBtn.disabled = i === 0;
      upBtn.addEventListener('click', function () { moveTask(i, -1); });

      var downBtn = document.createElement('button');
      downBtn.textContent = '\u2193';
      downBtn.className = 'todo-btn-sm';
      downBtn.title = '\u4E0B\u79FB';
      downBtn.disabled = i === tasks.length - 1;
      downBtn.addEventListener('click', function () { moveTask(i, 1); });

      var delBtn = document.createElement('button');
      delBtn.textContent = '\u00D7';
      delBtn.className = 'todo-btn-sm todo-btn-del';
      delBtn.title = '\u5220\u9664';
      delBtn.addEventListener('click', function () { removeTask(i); });

      actions.appendChild(upBtn);
      actions.appendChild(downBtn);
      actions.appendChild(delBtn);

      item.appendChild(checkbox);
      item.appendChild(label);
      item.appendChild(actions);
      list.appendChild(item);
    });
  }

  function renderToCanvas() {
    if (tasks.length === 0) {
      alert('请先添加任务');
      return;
    }

    var cv = document.getElementById('canvas');
    var context = cv.getContext('2d');
    var w = cv.width;
    var h = cv.height;

    context.fillStyle = '#FFFFFF';
    context.fillRect(0, 0, w, h);

    var title = document.getElementById('todo-title').value || '\u4ECA\u65E5\u5F85\u529E';
    var fontSize = parseInt(document.getElementById('todo-font-size').value) || 16;
    var colorMode = document.getElementById('todo-color-mode').value;
    var dateInput = document.getElementById('todo-date').value;
    var dateStr = '';
    if (dateInput) {
      var parts = dateInput.split('-');
      dateStr = parts[0] + '\u5E74' + parseInt(parts[1]) + '\u6708' + parseInt(parts[2]) + '\u65E5';
    } else {
      var now = new Date();
      dateStr = now.getFullYear() + '\u5E74' + (now.getMonth() + 1) + '\u6708' + now.getDate() + '\u65E5';
    }

    var accentColor = colorMode === 'redAccent' ? '#CC0000' : '#000000';
    var checkedColor = colorMode === 'redAccent' ? '#CC0000' : '#888888';
    var borderColor = '#000000';
    var textColor = '#000000';
    var lightColor = '#666666';

    var pad = Math.round(w * 0.06);
    var titleFontSize = Math.round(fontSize * 1.8);
    var dateFontSize = Math.round(fontSize * 0.8);
    var lineH = Math.round(fontSize * 2.0);
    var checkboxSize = Math.round(fontSize * 1.0);
    var fontFamily = '"PingFang SC", "Microsoft Yahei", "SimHei", sans-serif';

    var y = pad;

    context.textBaseline = 'top';

    context.fillStyle = accentColor;
    context.font = 'bold ' + titleFontSize + 'px ' + fontFamily;
    context.fillText(title, pad, y);
    y += titleFontSize + Math.round(fontSize * 0.3);

    var dayOfWeek = ['\u65E5', '\u4E00', '\u4E8C', '\u4E09', '\u56DB', '\u4E94', '\u516D'];
    var dateObj = dateInput ? new Date(dateInput) : new Date();
    var weekDay = '\u5468' + dayOfWeek[dateObj.getDay()];
    var dateFull = dateStr + ' ' + weekDay;

    context.fillStyle = lightColor;
    context.font = dateFontSize + 'px ' + fontFamily;
    context.fillText(dateFull, pad, y);
    y += dateFontSize + 4;

    var doneCount = tasks.filter(function (t) { return t.done; }).length;
    var progressText = '\u5B8C\u6210: ' + doneCount + '/' + tasks.length;
    context.fillStyle = accentColor;
    context.font = 'bold ' + dateFontSize + 'px ' + fontFamily;
    context.fillText(progressText, w - pad - context.measureText(progressText).width, y);
    y += dateFontSize + 6;

    context.strokeStyle = borderColor;
    context.lineWidth = 2;
    context.beginPath();
    context.moveTo(pad, y);
    context.lineTo(w - pad, y);
    context.stroke();
    y += Math.round(fontSize * 0.5);

    var progressBarY = y;
    var progressBarH = Math.round(fontSize * 0.25);
    var progress = tasks.length > 0 ? doneCount / tasks.length : 0;

    context.fillStyle = '#EEEEEE';
    context.fillRect(pad, progressBarY, w - pad * 2, progressBarH);

    if (progress > 0) {
      context.fillStyle = accentColor;
      context.fillRect(pad, progressBarY, (w - pad * 2) * progress, progressBarH);
    }

    context.strokeStyle = borderColor;
    context.lineWidth = 1;
    context.strokeRect(pad, progressBarY, w - pad * 2, progressBarH);
    y += progressBarH + Math.round(fontSize * 0.6);

    var maxItems = Math.floor((h - y - pad) / lineH);
    var displayTasks = tasks.slice(0, maxItems);

    displayTasks.forEach(function (task, index) {
      var boxX = pad + 2;
      var boxY = y + (lineH - checkboxSize) / 2;

      var seqX = pad;
      context.fillStyle = lightColor;
      context.font = Math.round(fontSize * 0.7) + 'px ' + fontFamily;
      var seqText = String(index + 1).padStart(2, '0') + '.';
      context.textBaseline = 'middle';
      context.fillText(seqText, seqX, y + lineH / 2);

      boxX = pad + Math.round(fontSize * 1.4);
      var textX = boxX + checkboxSize + Math.round(fontSize * 0.5);
      var textY = y + lineH / 2;

      if (task.done) {
        context.fillStyle = accentColor;
        context.fillRect(boxX, boxY, checkboxSize, checkboxSize);

        context.strokeStyle = '#FFFFFF';
        context.lineWidth = 2.5;
        context.lineCap = 'round';
        context.lineJoin = 'round';
        context.beginPath();
        context.moveTo(boxX + Math.round(checkboxSize * 0.15), boxY + Math.round(checkboxSize * 0.55));
        context.lineTo(boxX + Math.round(checkboxSize * 0.4), boxY + Math.round(checkboxSize * 0.8));
        context.lineTo(boxX + Math.round(checkboxSize * 0.85), boxY + Math.round(checkboxSize * 0.2));
        context.stroke();

        context.fillStyle = textColor;
        context.font = fontSize + 'px ' + fontFamily;
        context.textBaseline = 'middle';

        var tw = context.measureText(task.text).width;
        var maxTextWidth = w - textX - pad;
        var displayText = task.text;
        if (tw > maxTextWidth) {
          while (context.measureText(displayText).width > maxTextWidth && displayText.length > 1) {
            displayText = displayText.slice(0, -1);
          }
          displayText = displayText.slice(0, -1) + '...';
        }
        context.fillText(displayText, textX, textY);

        var lineStartX = textX;
        var lineEndX = textX + context.measureText(displayText).width;
        var lineY = textY + Math.round(fontSize * 0.08);
        context.strokeStyle = textColor;
        context.lineWidth = 1.2;
        context.lineCap = 'butt';
        context.beginPath();
        context.moveTo(lineStartX, lineY);
        context.lineTo(lineEndX, lineY);
        context.stroke();
      } else {
        context.strokeStyle = borderColor;
        context.lineWidth = 1.5;
        context.strokeRect(boxX, boxY, checkboxSize, checkboxSize);

        context.fillStyle = textColor;
        context.font = fontSize + 'px ' + fontFamily;
        context.textBaseline = 'middle';

        var displayText = task.text;
        var maxTextWidth = w - textX - pad;
        while (context.measureText(displayText).width > maxTextWidth && displayText.length > 1) {
          displayText = displayText.slice(0, -1);
        }
        if (displayText !== task.text) {
          displayText = displayText.slice(0, -1) + '...';
        }
        context.fillText(displayText, textX, textY);
      }

      y += lineH;
    });

    if (tasks.length > maxItems) {
      context.fillStyle = lightColor;
      context.font = Math.round(fontSize * 0.8) + 'px ' + fontFamily;
      context.textBaseline = 'top';
      context.fillText('\u8FD8\u6709 ' + (tasks.length - maxItems) + ' \u9879\u672A\u663E\u793A...', pad, y + 4);
    }

    context.strokeStyle = borderColor;
    context.lineWidth = 2;
    context.strokeRect(1, 1, w - 2, h - 2);

    context.textBaseline = 'alphabetic';

    if (typeof paintManager !== 'undefined') {
      paintManager.clearHistory();
      paintManager.saveToHistory();
    }

    addLog('\u4EFB\u52A1\u6E05\u5355\u5DF2\u751F\u6210\u5230\u753B\u5E03');
  }

  function renderAndSend() {
    renderToCanvas();
    if (typeof sendimg === 'function') {
      setTimeout(function () {
        sendimg();
      }, 300);
    }
  }

  document.addEventListener('DOMContentLoaded', function () {
    var todoInput = document.getElementById('todo-input');
    if (todoInput) {
      todoInput.addEventListener('keydown', function (e) {
        if (e.key === 'Enter') {
          e.preventDefault();
          addTask();
        }
      });
    }

    var today = new Date();
    var dateInput = document.getElementById('todo-date');
    if (dateInput && !dateInput.value) {
      var yyyy = today.getFullYear();
      var mm = String(today.getMonth() + 1).padStart(2, '0');
      var dd = String(today.getDate()).padStart(2, '0');
      dateInput.value = yyyy + '-' + mm + '-' + dd;
    }

    renderList();
  });

  function loadExamples() {
    tasks = defaultTasks.map(function (t) { return { text: t.text, done: t.done }; });
    saveTasks();
    renderList();
    triggerAutoSend();
  }

  function setTasks(newTasks) {
    tasks = newTasks.map(function (t) { return { text: t.text || t.title || '', done: !!t.done }; });
    saveTasks();
    renderList();
  }

  function triggerAutoSend() {
    if (typeof GoChartManager !== 'undefined' && GoChartManager.isAutoSendEnabled()) {
      addLog('任务变更，自动生成并发送...');
      setTimeout(function () {
        renderAndSend();
      }, 300);
    }
  }

  return {
    addTask: addTask,
    toggleTask: toggleTask,
    removeTask: removeTask,
    clearAll: clearAll,
    loadExamples: loadExamples,
    renderList: renderList,
    renderToCanvas: renderToCanvas,
    renderAndSend: renderAndSend,
    setTasks: setTasks,
    triggerAutoSend: triggerAutoSend
  };
})();
