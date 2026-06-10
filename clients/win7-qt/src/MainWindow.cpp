#include "MainWindow.h"

#include "SnapshotScanner.h"
#include "TargetConnector.h"

#include <QAction>
#include <QApplication>
#include <QAbstractItemView>
#include <QBoxLayout>
#include <QCloseEvent>
#include <QDateTime>
#include <QDialog>
#include <QDialogButtonBox>
#include <QFile>
#include <QFileDialog>
#include <QFileInfo>
#include <QFormLayout>
#include <QFont>
#include <QGroupBox>
#include <QHeaderView>
#include <QIODevice>
#include <QLabel>
#include <QLineEdit>
#include <QMenu>
#include <QMessageBox>
#include <QPlainTextEdit>
#include <QPushButton>
#include <QRegExp>
#include <QSettings>
#include <QStandardPaths>
#include <QStyle>
#include <QSystemTrayIcon>
#include <QTableWidget>
#include <QTableWidgetItem>
#include <QTextEdit>
#include <QThread>
#include <QUuid>

namespace {
enum TaskColumn {
    ColumnType = 0,
    ColumnName,
    ColumnStatus,
    ColumnDevices,
    ColumnLocalSize,
    ColumnGlobalSize,
    ColumnReceive,
    ColumnSend,
    ColumnCount
};

QString columnTitle(int column)
{
    switch (column) {
    case ColumnType:
        return QStringLiteral("类型");
    case ColumnName:
        return QStringLiteral("名称");
    case ColumnStatus:
        return QStringLiteral("状态");
    case ColumnDevices:
        return QStringLiteral("同步设备");
    case ColumnLocalSize:
        return QStringLiteral("本地大小");
    case ColumnGlobalSize:
        return QStringLiteral("全局大小");
    case ColumnReceive:
        return QStringLiteral("接收");
    case ColumnSend:
        return QStringLiteral("发送");
    default:
        return QString();
    }
}

QString newTaskID()
{
    return QUuid::createUuid().toString(QUuid::WithoutBraces);
}
} // namespace

MainWindow::MainWindow(QWidget* parent)
    : QMainWindow(parent)
{
    setWindowTitle(QStringLiteral("OneSync Win7"));
    resize(980, 620);

    buildUi();
    setupTray();
    loadTasks();
    refreshTaskTable();
    appendLog(QStringLiteral("OneSync Win7 Qt 客户端已启动。"));
}

void MainWindow::buildUi()
{
    auto* root = new QWidget(this);
    auto* layout = new QVBoxLayout(root);

    auto* titleRow = new QHBoxLayout();
    auto* title = new QLabel(QStringLiteral("同步任务"));
    QFont titleFont = title->font();
    titleFont.setPointSize(titleFont.pointSize() + 5);
    titleFont.setBold(true);
    title->setFont(titleFont);
    summaryLabel = new QLabel(QStringLiteral("0 个任务"));
    summaryLabel->setAlignment(Qt::AlignRight | Qt::AlignVCenter);
    titleRow->addWidget(title);
    titleRow->addStretch(1);
    titleRow->addWidget(summaryLabel);
    layout->addLayout(titleRow);

    auto* toolbar = new QHBoxLayout();
    startButton = new QPushButton(QStringLiteral("开始"));
    pauseButton = new QPushButton(QStringLiteral("暂停"));
    rescanButton = new QPushButton(QStringLiteral("重新扫描"));
    parametersButton = new QPushButton(QStringLiteral("参数"));
    deleteButton = new QPushButton(QStringLiteral("删除"));
    auto* addButton = new QPushButton(QStringLiteral("加入同步"));
    auto* diagnosticsButton = new QPushButton(QStringLiteral("导出诊断"));

    connect(startButton, &QPushButton::clicked, this, &MainWindow::startSelectedTask);
    connect(pauseButton, &QPushButton::clicked, this, &MainWindow::pauseSelectedTask);
    connect(rescanButton, &QPushButton::clicked, this, &MainWindow::rescanSelectedTask);
    connect(parametersButton, &QPushButton::clicked, this, &MainWindow::editSelectedTask);
    connect(deleteButton, &QPushButton::clicked, this, &MainWindow::deleteSelectedTask);
    connect(addButton, &QPushButton::clicked, this, &MainWindow::addTask);
    connect(diagnosticsButton, &QPushButton::clicked, this, &MainWindow::exportDiagnostics);

    toolbar->addWidget(startButton);
    toolbar->addWidget(pauseButton);
    toolbar->addWidget(rescanButton);
    toolbar->addWidget(parametersButton);
    toolbar->addWidget(deleteButton);
    toolbar->addStretch(1);
    toolbar->addWidget(addButton);
    toolbar->addWidget(diagnosticsButton);
    layout->addLayout(toolbar);

    taskTable = new QTableWidget(0, ColumnCount, this);
    taskTable->setSelectionBehavior(QAbstractItemView::SelectRows);
    taskTable->setSelectionMode(QAbstractItemView::SingleSelection);
    taskTable->setEditTriggers(QAbstractItemView::NoEditTriggers);
    taskTable->verticalHeader()->setVisible(false);
    taskTable->horizontalHeader()->setStretchLastSection(true);
    taskTable->horizontalHeader()->setSectionResizeMode(QHeaderView::Interactive);
    for (int column = 0; column < ColumnCount; ++column) {
        taskTable->setHorizontalHeaderItem(column, new QTableWidgetItem(columnTitle(column)));
    }
    taskTable->setColumnWidth(ColumnType, 80);
    taskTable->setColumnWidth(ColumnName, 190);
    taskTable->setColumnWidth(ColumnStatus, 150);
    taskTable->setColumnWidth(ColumnDevices, 90);
    taskTable->setColumnWidth(ColumnLocalSize, 100);
    taskTable->setColumnWidth(ColumnGlobalSize, 100);
    taskTable->setColumnWidth(ColumnReceive, 90);
    taskTable->setColumnWidth(ColumnSend, 90);
    connect(taskTable, &QTableWidget::itemSelectionChanged, this, &MainWindow::refreshButtons);
    connect(taskTable, &QTableWidget::cellDoubleClicked, this, [this](int, int) {
        editSelectedTask();
    });
    layout->addWidget(taskTable, 1);

    auto* logBox = new QGroupBox(QStringLiteral("日志"));
    auto* logLayout = new QVBoxLayout(logBox);
    logEdit = new QPlainTextEdit();
    logEdit->setReadOnly(true);
    logEdit->setPlaceholderText(QStringLiteral("运行日志会显示在这里。"));
    logLayout->addWidget(logEdit);
    layout->addWidget(logBox, 1);

    setCentralWidget(root);
    refreshButtons();
}

void MainWindow::addTask()
{
    SyncTask task;
    task.id = newTaskID();
    task.name = QStringLiteral("接收任务");
    task.status = QStringLiteral("停止");
    task.detail = QStringLiteral("尚未启动");
    if (!runTaskDialog(&task, false)) {
        return;
    }
    tasks.append(task);
    saveTasks();
    refreshTaskTable();
    taskTable->selectRow(tasks.size() - 1);
    appendLog(QStringLiteral("已加入同步任务：%1").arg(task.name));
}

void MainWindow::startSelectedTask()
{
    SyncTask* task = selectedTask();
    if (task == nullptr) {
        return;
    }
    if (task->running) {
        QMessageBox::information(this, QStringLiteral("任务正在运行"), QStringLiteral("这个任务已经在运行。"));
        return;
    }
    QString error;
    if (!parseTaskLink(task, &error)) {
        task->status = QStringLiteral("链接无效");
        task->detail = error;
        refreshTaskTable();
        QMessageBox::warning(this, QStringLiteral("同步链接不可用"), error);
        return;
    }
    const QFileInfo targetInfo(task->targetFolder);
    if (!targetInfo.exists() || !targetInfo.isDir()) {
        QMessageBox::warning(this, QStringLiteral("目录不可用"), QStringLiteral("接收文件夹不存在或不是目录。"));
        return;
    }

    const QString taskID = task->id;
    task->running = true;
    task->connectedDevices = 0;
    task->receivedBytes = 0;
    task->sentBytes = 0;
    task->status = QStringLiteral("运行-连接中");
    task->detail = QStringLiteral("正在连接源端");
    refreshTaskTable();
    appendLog(QStringLiteral("[%1] 开始连接源端。").arg(task->name));

    QThread* thread = new QThread(this);
    auto* connector = new TargetConnector(task->link, task->targetFolder);
    connector->moveToThread(thread);
    connectionThreads.insert(taskID, thread);

    connect(thread, &QThread::started, connector, &TargetConnector::run);
    connect(connector, &TargetConnector::logMessage, this, [this, taskID](const QString& message) {
        const SyncTask* task = nullptr;
        for (const SyncTask& item : tasks) {
            if (item.id == taskID) {
                task = &item;
                break;
            }
        }
        appendLog(QStringLiteral("[%1] %2").arg(task != nullptr ? task->name : taskID, message));
    });
    connect(connector, &TargetConnector::statusChanged, this, [this, taskID](const QString& status) {
        setTaskStatus(taskID, status);
    });
    connect(connector, &TargetConnector::finished, this, [this, taskID](bool ok, const QString& message) {
        for (SyncTask& item : tasks) {
            if (item.id != taskID) {
                continue;
            }
            item.running = false;
            item.connectedDevices = ok ? 1 : 0;
            item.status = ok ? QStringLiteral("运行-本轮完成") : QStringLiteral("失败");
            item.detail = message;
            appendLog(QStringLiteral("[%1] %2").arg(item.name, message));
            break;
        }
        saveTasks();
        refreshTaskTable();
        if (!ok) {
            QMessageBox::warning(this, QStringLiteral("同步失败"), message);
        }
    });
    connect(connector, &TargetConnector::finished, thread, &QThread::quit);
    connect(connector, &TargetConnector::finished, connector, &TargetConnector::deleteLater);
    connect(thread, &QThread::finished, thread, &QThread::deleteLater);
    connect(thread, &QThread::finished, this, [this, taskID]() {
        connectionThreads.remove(taskID);
        refreshButtons();
    });

    thread->start();
}

void MainWindow::pauseSelectedTask()
{
    SyncTask* task = selectedTask();
    if (task == nullptr) {
        return;
    }
    if (task->running) {
        QMessageBox::information(
            this,
            QStringLiteral("暂不能强制中断"),
            QStringLiteral("Win7 兼容客户端当前不会强制中断正在传输的文件。本轮同步结束后会回到可操作状态。")
        );
        return;
    }
    task->status = QStringLiteral("停止");
    task->detail = QStringLiteral("已停止");
    task->connectedDevices = 0;
    saveTasks();
    refreshTaskTable();
    appendLog(QStringLiteral("[%1] 已停止。").arg(task->name));
}

void MainWindow::rescanSelectedTask()
{
    SyncTask* task = selectedTask();
    if (task == nullptr) {
        return;
    }
    QString error;
    QByteArray snapshotJson;
    quint64 fileCount = 0;
    quint64 byteCount = 0;
    appendLog(QStringLiteral("[%1] 开始重新扫描本地目录。").arg(task->name));
    if (!SnapshotScanner::scanToJson(task->targetFolder, &snapshotJson, &fileCount, &byteCount, &error)) {
        task->status = QStringLiteral("扫描失败");
        task->detail = error;
        refreshTaskTable();
        QMessageBox::warning(this, QStringLiteral("扫描失败"), error);
        return;
    }
    task->localFiles = int(fileCount);
    task->localBytes = byteCount;
    if (task->globalBytes == 0) {
        task->globalBytes = byteCount;
    }
    task->detail = QStringLiteral("本地 %1 个文件").arg(fileCount);
    saveTasks();
    refreshTaskTable();
    appendLog(QStringLiteral("[%1] 扫描完成：%2 个文件，%3。").arg(task->name).arg(fileCount).arg(formatBytes(byteCount)));
}

void MainWindow::editSelectedTask()
{
    SyncTask* task = selectedTask();
    if (task == nullptr) {
        return;
    }
    if (task->running) {
        QMessageBox::information(this, QStringLiteral("任务正在运行"), QStringLiteral("运行中的任务暂不能修改参数。"));
        return;
    }
    showTaskParameters(task);
}

void MainWindow::deleteSelectedTask()
{
    const int index = selectedTaskIndex();
    if (index < 0) {
        return;
    }
    if (tasks[index].running) {
        QMessageBox::information(this, QStringLiteral("任务正在运行"), QStringLiteral("请等待本轮同步结束后再删除。"));
        return;
    }
    const QString name = tasks[index].name;
    if (QMessageBox::question(this, QStringLiteral("删除同步任务"), QStringLiteral("确定删除任务“%1”吗？本地文件不会被删除。").arg(name)) != QMessageBox::Yes) {
        return;
    }
    tasks.removeAt(index);
    saveTasks();
    refreshTaskTable();
    appendLog(QStringLiteral("已删除同步任务：%1").arg(name));
}

void MainWindow::exportDiagnostics()
{
    const QString defaultDir = QStandardPaths::writableLocation(QStandardPaths::DesktopLocation);
    const QString fileName = QFileDialog::getSaveFileName(
        this,
        QStringLiteral("保存诊断日志"),
        defaultDir + QStringLiteral("/onesync-win7-diagnostics.txt"),
        QStringLiteral("Text files (*.txt)")
    );
    if (fileName.isEmpty()) {
        return;
    }
    QFile file(fileName);
    if (!file.open(QIODevice::WriteOnly | QIODevice::Text)) {
        QMessageBox::warning(this, QStringLiteral("保存失败"), file.errorString());
        return;
    }
    file.write(diagnosticsText().toUtf8());
    appendLog(QStringLiteral("诊断日志已保存：%1").arg(fileName));
}

void MainWindow::showFromTray()
{
    showNormal();
    raise();
    activateWindow();
}

void MainWindow::quitFromTray()
{
    if (!connectionThreads.isEmpty()) {
        QMessageBox::information(this, QStringLiteral("正在同步"), QStringLiteral("当前仍有同步任务在运行，请完成后再退出。"));
        return;
    }
    exiting = true;
    qApp->quit();
}

void MainWindow::closeEvent(QCloseEvent* event)
{
    if (exiting || trayIcon == nullptr || !trayIcon->isVisible()) {
        QMainWindow::closeEvent(event);
        return;
    }
    hide();
    event->ignore();
    if (!trayCloseTipShown) {
        trayCloseTipShown = true;
        trayIcon->showMessage(
            QStringLiteral("OneSync 仍在运行"),
            QStringLiteral("窗口已最小化到托盘。右键托盘图标可以显示或退出。"),
            QSystemTrayIcon::Information,
            3000
        );
    }
}

void MainWindow::setupTray()
{
    if (!QSystemTrayIcon::isSystemTrayAvailable()) {
        appendLog(QStringLiteral("系统托盘不可用。"));
        return;
    }

    trayMenu = new QMenu(this);
    auto* showAction = new QAction(QStringLiteral("显示 OneSync"), this);
    auto* quitAction = new QAction(QStringLiteral("退出"), this);
    connect(showAction, &QAction::triggered, this, &MainWindow::showFromTray);
    connect(quitAction, &QAction::triggered, this, &MainWindow::quitFromTray);
    trayMenu->addAction(showAction);
    trayMenu->addSeparator();
    trayMenu->addAction(quitAction);

    trayIcon = new QSystemTrayIcon(this);
    trayIcon->setIcon(style()->standardIcon(QStyle::SP_ComputerIcon));
    trayIcon->setToolTip(QStringLiteral("OneSync Win7"));
    trayIcon->setContextMenu(trayMenu);
    connect(trayIcon, &QSystemTrayIcon::activated, this, [this](QSystemTrayIcon::ActivationReason reason) {
        if (reason == QSystemTrayIcon::Trigger || reason == QSystemTrayIcon::DoubleClick) {
            showFromTray();
        }
    });
    trayIcon->show();
    appendLog(QStringLiteral("系统托盘已启用。"));
}

void MainWindow::loadTasks()
{
    tasks.clear();
    QSettings settings(QStringLiteral("OneSync"), QStringLiteral("OneSyncWin7"));
    const int count = settings.beginReadArray(QStringLiteral("tasks"));
    for (int index = 0; index < count; ++index) {
        settings.setArrayIndex(index);
        SyncTask task;
        task.id = settings.value(QStringLiteral("id"), newTaskID()).toString();
        task.name = settings.value(QStringLiteral("name"), QStringLiteral("接收任务")).toString();
        task.linkText = settings.value(QStringLiteral("link")).toString();
        task.targetFolder = settings.value(QStringLiteral("targetFolder")).toString();
        task.ignoreRules = settings.value(QStringLiteral("ignoreRules")).toStringList();
        task.localBytes = settings.value(QStringLiteral("localBytes")).toULongLong();
        task.globalBytes = settings.value(QStringLiteral("globalBytes")).toULongLong();
        task.localFiles = settings.value(QStringLiteral("localFiles")).toInt();
        task.status = QStringLiteral("停止");
        task.detail = QStringLiteral("尚未启动");
        QString error;
        if (!parseTaskLink(&task, &error)) {
            task.status = QStringLiteral("链接无效");
            task.detail = error;
        }
        tasks.append(task);
    }
    settings.endArray();
}

void MainWindow::saveTasks() const
{
    QSettings settings(QStringLiteral("OneSync"), QStringLiteral("OneSyncWin7"));
    settings.beginWriteArray(QStringLiteral("tasks"));
    for (int index = 0; index < tasks.size(); ++index) {
        const SyncTask& task = tasks[index];
        settings.setArrayIndex(index);
        settings.setValue(QStringLiteral("id"), task.id);
        settings.setValue(QStringLiteral("name"), task.name);
        settings.setValue(QStringLiteral("link"), task.linkText);
        settings.setValue(QStringLiteral("targetFolder"), task.targetFolder);
        settings.setValue(QStringLiteral("ignoreRules"), task.ignoreRules);
        settings.setValue(QStringLiteral("localBytes"), task.localBytes);
        settings.setValue(QStringLiteral("globalBytes"), task.globalBytes);
        settings.setValue(QStringLiteral("localFiles"), task.localFiles);
    }
    settings.endArray();
}

void MainWindow::refreshTaskTable()
{
    const int selected = selectedTaskIndex();
    taskTable->setRowCount(tasks.size());
    for (int row = 0; row < tasks.size(); ++row) {
        const SyncTask& task = tasks[row];
        const QString devices = QStringLiteral("%1 / %2").arg(task.connectedDevices).arg(task.totalDevices);
        const QStringList values = {
            QStringLiteral("接收"),
            task.name,
            task.status,
            devices,
            task.localBytes > 0 ? formatBytes(task.localBytes) : QStringLiteral("-"),
            task.globalBytes > 0 ? formatBytes(task.globalBytes) : QStringLiteral("-"),
            formatRate(task.receivedBytes),
            formatRate(task.sentBytes)
        };
        for (int column = 0; column < values.size(); ++column) {
            auto* item = taskTable->item(row, column);
            if (item == nullptr) {
                item = new QTableWidgetItem();
                taskTable->setItem(row, column, item);
            }
            item->setText(values[column]);
            item->setToolTip(task.detail);
        }
    }
    summaryLabel->setText(QStringLiteral("%1 个任务").arg(tasks.size()));
    if (selected >= 0 && selected < tasks.size()) {
        taskTable->selectRow(selected);
    }
    refreshButtons();
}

void MainWindow::refreshButtons()
{
    const SyncTask* task = selectedTask();
    const bool hasSelection = task != nullptr;
    startButton->setEnabled(hasSelection && !task->running);
    pauseButton->setEnabled(hasSelection);
    rescanButton->setEnabled(hasSelection);
    parametersButton->setEnabled(hasSelection);
    deleteButton->setEnabled(hasSelection && !task->running);
}

int MainWindow::selectedTaskIndex() const
{
    if (taskTable == nullptr) {
        return -1;
    }
    const QList<QTableWidgetItem*> selected = taskTable->selectedItems();
    if (selected.isEmpty()) {
        return -1;
    }
    const int row = selected.first()->row();
    return row >= 0 && row < tasks.size() ? row : -1;
}

MainWindow::SyncTask* MainWindow::selectedTask()
{
    const int index = selectedTaskIndex();
    return index >= 0 ? &tasks[index] : nullptr;
}

const MainWindow::SyncTask* MainWindow::selectedTask() const
{
    const int index = selectedTaskIndex();
    return index >= 0 ? &tasks[index] : nullptr;
}

void MainWindow::setTaskStatus(const QString& taskID, const QString& status, const QString& detail)
{
    for (SyncTask& task : tasks) {
        if (task.id != taskID) {
            continue;
        }
        task.status = status;
        if (!detail.isEmpty()) {
            task.detail = detail;
        }
        if (status == QStringLiteral("运行-已连接源端")) {
            task.connectedDevices = 1;
        }
        refreshTaskTable();
        return;
    }
}

bool MainWindow::parseTaskLink(SyncTask* task, QString* error)
{
    if (task == nullptr) {
        if (error != nullptr) {
            *error = QStringLiteral("内部错误：任务为空。");
        }
        return false;
    }
    SyncLink parsed;
    if (!SyncLinkParser::parse(task->linkText, &parsed, error)) {
        task->linkReady = false;
        return false;
    }
    task->link = parsed;
    task->linkReady = true;
    return true;
}

bool MainWindow::runTaskDialog(SyncTask* task, bool editing)
{
    QDialog dialog(this);
    dialog.setWindowTitle(editing ? QStringLiteral("任务参数") : QStringLiteral("加入同步"));
    auto* layout = new QVBoxLayout(&dialog);
    auto* form = new QFormLayout();
    auto* nameEdit = new QLineEdit(task->name);
    auto* folderEdit = new QLineEdit(task->targetFolder);
    auto* linkEdit = new QTextEdit(task->linkText);
    linkEdit->setAcceptRichText(false);
    linkEdit->setPlaceholderText(QStringLiteral("粘贴源端生成的同步链接"));

    auto* folderRow = new QHBoxLayout();
    auto* chooseButton = new QPushButton(QStringLiteral("选择目录"));
    folderRow->addWidget(folderEdit);
    folderRow->addWidget(chooseButton);
    connect(chooseButton, &QPushButton::clicked, &dialog, [&dialog, folderEdit]() {
        const QString folder = QFileDialog::getExistingDirectory(&dialog, QStringLiteral("选择接收文件夹"), folderEdit->text());
        if (!folder.isEmpty()) {
            folderEdit->setText(folder);
        }
    });

    form->addRow(QStringLiteral("名称"), nameEdit);
    form->addRow(QStringLiteral("接收目录"), folderRow);
    form->addRow(QStringLiteral("同步链接"), linkEdit);
    layout->addLayout(form);

    auto* hint = new QLabel(QStringLiteral("Win7 兼容客户端当前作为目标端使用：粘贴链接后，会从源端接收文件。"));
    hint->setWordWrap(true);
    layout->addWidget(hint);

    auto* buttons = new QDialogButtonBox(QDialogButtonBox::Ok | QDialogButtonBox::Cancel);
    buttons->button(QDialogButtonBox::Ok)->setText(editing ? QStringLiteral("保存") : QStringLiteral("加入"));
    buttons->button(QDialogButtonBox::Cancel)->setText(QStringLiteral("取消"));
    layout->addWidget(buttons);

    connect(buttons, &QDialogButtonBox::accepted, &dialog, &QDialog::accept);
    connect(buttons, &QDialogButtonBox::rejected, &dialog, &QDialog::reject);

    if (dialog.exec() != QDialog::Accepted) {
        return false;
    }

    const QString name = nameEdit->text().trimmed();
    const QString folder = folderEdit->text().trimmed();
    const QString link = linkEdit->toPlainText().trimmed();
    if (name.isEmpty()) {
        QMessageBox::warning(this, QStringLiteral("名称为空"), QStringLiteral("请填写任务名称。"));
        return false;
    }
    if (folder.isEmpty() || !QFileInfo(folder).isDir()) {
        QMessageBox::warning(this, QStringLiteral("目录不可用"), QStringLiteral("请选择已经存在的接收文件夹。"));
        return false;
    }
    task->name = name;
    task->targetFolder = folder;
    task->linkText = link;
    QString error;
    if (!parseTaskLink(task, &error)) {
        QMessageBox::warning(this, QStringLiteral("同步链接无效"), error);
        return false;
    }
    task->status = QStringLiteral("停止");
    task->detail = task->link.hasRelay()
        ? QStringLiteral("Relay：%1").arg(task->link.relayEndpoint)
        : QStringLiteral("源端：%1").arg(task->link.endpoint);
    return true;
}

void MainWindow::showTaskParameters(SyncTask* task)
{
    if (task == nullptr) {
        return;
    }
    QDialog dialog(this);
    dialog.setWindowTitle(QStringLiteral("任务参数"));
    auto* layout = new QVBoxLayout(&dialog);

    auto* editButton = new QPushButton(QStringLiteral("修改名称、目录和链接"));
    layout->addWidget(editButton);
    connect(editButton, &QPushButton::clicked, &dialog, [this, task]() {
        if (runTaskDialog(task, true)) {
            saveTasks();
            refreshTaskTable();
            appendLog(QStringLiteral("[%1] 任务参数已更新。").arg(task->name));
        }
    });

    auto* ignoreLabel = new QLabel(QStringLiteral("忽略规则（每行一条，当前先保存规则；后续会接入扫描和同步过滤）："));
    ignoreLabel->setWordWrap(true);
    layout->addWidget(ignoreLabel);
    auto* ignoreEdit = new QTextEdit(task->ignoreRules.join(QStringLiteral("\n")));
    ignoreEdit->setAcceptRichText(false);
    ignoreEdit->setPlaceholderText(QStringLiteral("例如：\n*.tmp\n.cache/\nnode_modules/"));
    layout->addWidget(ignoreEdit, 1);

    auto* buttons = new QDialogButtonBox(QDialogButtonBox::Ok | QDialogButtonBox::Cancel);
    buttons->button(QDialogButtonBox::Ok)->setText(QStringLiteral("保存"));
    buttons->button(QDialogButtonBox::Cancel)->setText(QStringLiteral("取消"));
    layout->addWidget(buttons);
    connect(buttons, &QDialogButtonBox::accepted, &dialog, &QDialog::accept);
    connect(buttons, &QDialogButtonBox::rejected, &dialog, &QDialog::reject);

    if (dialog.exec() != QDialog::Accepted) {
        return;
    }

    QStringList rules;
    const QStringList lines = ignoreEdit->toPlainText().split(QRegExp(QStringLiteral("[\r\n]+")), QString::SkipEmptyParts);
    for (const QString& line : lines) {
        const QString rule = line.trimmed();
        if (!rule.isEmpty()) {
            rules.append(rule);
        }
    }
    task->ignoreRules = rules;
    saveTasks();
    refreshTaskTable();
    appendLog(QStringLiteral("[%1] 忽略规则已保存：%2 条。").arg(task->name).arg(rules.size()));
}

QString MainWindow::taskDiagnosticsText(const SyncTask& task) const
{
    QString text;
    text += QStringLiteral("任务: %1\n").arg(task.name);
    text += QStringLiteral("类型: 接收\n");
    text += QStringLiteral("状态: %1\n").arg(task.status);
    text += QStringLiteral("详情: %1\n").arg(task.detail);
    text += QStringLiteral("接收目录: %1\n").arg(task.targetFolder);
    text += QStringLiteral("源端地址: %1\n").arg(task.linkReady ? task.link.endpoint : QStringLiteral("-"));
    text += QStringLiteral("Relay 地址: %1\n").arg(task.linkReady && task.link.hasRelay() ? task.link.relayEndpoint : QStringLiteral("-"));
    text += QStringLiteral("会话编号: %1\n").arg(task.linkReady ? task.link.sessionId : QStringLiteral("-"));
    text += QStringLiteral("本地大小: %1\n").arg(formatBytes(task.localBytes));
    text += QStringLiteral("全局大小: %1\n").arg(task.globalBytes > 0 ? formatBytes(task.globalBytes) : QStringLiteral("-"));
    text += QStringLiteral("忽略规则: %1 条\n").arg(task.ignoreRules.size());
    text += QStringLiteral("\n");
    return text;
}

QString MainWindow::formatBytes(quint64 value) const
{
    const char* units[] = {"B", "KB", "MB", "GB", "TB"};
    double size = double(value);
    int unit = 0;
    while (size >= 1024.0 && unit < 4) {
        size /= 1024.0;
        ++unit;
    }
    if (unit == 0) {
        return QStringLiteral("%1 B").arg(value);
    }
    return QStringLiteral("%1 %2").arg(size, 0, 'f', size >= 10.0 ? 1 : 2).arg(QString::fromLatin1(units[unit]));
}

QString MainWindow::formatRate(quint64 value) const
{
    if (value == 0) {
        return QStringLiteral("0 B/s");
    }
    return formatBytes(value) + QStringLiteral("/s");
}

void MainWindow::appendLog(const QString& message)
{
    const QString line = QStringLiteral("[%1] %2")
        .arg(QDateTime::currentDateTime().toString(Qt::ISODate))
        .arg(message);
    logEdit->appendPlainText(line);
}

QString MainWindow::diagnosticsText() const
{
    QString text;
    text += QStringLiteral("OneSync Win7 Qt 诊断日志\n");
    text += QStringLiteral("生成时间: %1\n").arg(QDateTime::currentDateTimeUtc().toString(Qt::ISODate));
    text += QStringLiteral("任务数量: %1\n\n").arg(tasks.size());
    for (const SyncTask& task : tasks) {
        text += taskDiagnosticsText(task);
    }
    text += QStringLiteral("运行日志:\n");
    text += logEdit->toPlainText();
    text += QStringLiteral("\n");
    return text;
}
