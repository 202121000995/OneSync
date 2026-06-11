#include "MainWindow.h"

#include "Endpoint.h"
#include "IgnoreMatcher.h"
#include "SnapshotScanner.h"
#include "SourceConnector.h"
#include "TargetConnector.h"

#include <QAction>
#include <QApplication>
#include <QAbstractItemView>
#include <QBoxLayout>
#include <QClipboard>
#include <QCloseEvent>
#include <QComboBox>
#include <QCryptographicHash>
#include <QDateTime>
#include <QDialog>
#include <QDialogButtonBox>
#include <QFile>
#include <QFileDialog>
#include <QFileInfo>
#include <QFormLayout>
#include <QFont>
#include <QFrame>
#include <QGroupBox>
#include <QHeaderView>
#include <QIODevice>
#include <QInputDialog>
#include <QJsonDocument>
#include <QJsonObject>
#include <QLabel>
#include <QLineEdit>
#include <QMenu>
#include <QMessageBox>
#include <QPlainTextEdit>
#include <QPushButton>
#include <QRandomGenerator>
#include <QRegExp>
#include <QSettings>
#include <QSslCertificate>
#include <QSslSocket>
#include <QStandardPaths>
#include <QStackedWidget>
#include <QStyle>
#include <QStyleFactory>
#include <QSystemTrayIcon>
#include <QTableWidget>
#include <QTableWidgetItem>
#include <QTextCursor>
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

QByteArray randomBytes(int size)
{
    QByteArray data(size, Qt::Uninitialized);
    QRandomGenerator* random = QRandomGenerator::system();
    for (int index = 0; index < size; ++index) {
        data[index] = char(random->bounded(256));
    }
    return data;
}

QString base64Url(const QByteArray& data)
{
    return QString::fromLatin1(data.toBase64(QByteArray::Base64UrlEncoding | QByteArray::OmitTrailingEquals));
}

QByteArray certificateFingerprint(const QSslCertificate& certificate)
{
    return certificate.digest(QCryptographicHash::Sha256).toHex();
}

bool certificateMatchesPinned(const QSslCertificate& certificate, const QList<QSslCertificate>& pinnedCertificates)
{
    if (certificate.isNull()) {
        return false;
    }
    const QByteArray fingerprint = certificateFingerprint(certificate);
    for (const QSslCertificate& pinned : pinnedCertificates) {
        if (certificateFingerprint(pinned) == fingerprint) {
            return true;
        }
    }
    return false;
}

QPushButton* toolbarButton(const QString& text, const QString& objectName = QString())
{
    auto* button = new QPushButton(text);
    button->setMinimumHeight(34);
    button->setCursor(Qt::PointingHandCursor);
    if (!objectName.isEmpty()) {
        button->setObjectName(objectName);
    }
    return button;
}

void applyModernDialogStyle(QDialog* dialog)
{
    dialog->setObjectName(QStringLiteral("modernDialog"));
    dialog->setStyleSheet(QStringLiteral(R"(
        QDialog#modernDialog {
            background: #f4f7fb;
            color: #1f2937;
            font-family: "Microsoft YaHei", "Segoe UI", sans-serif;
            font-size: 10.5pt;
        }
        QLabel {
            color: #334155;
            font-weight: 600;
        }
        QLineEdit, QTextEdit {
            background: #ffffff;
            border: 1px solid #cfd9e8;
            border-radius: 10px;
            padding: 8px 10px;
            selection-background-color: #bfdbfe;
        }
        QLineEdit:focus, QTextEdit:focus {
            border-color: #2563eb;
        }
        QPushButton {
            background: #ffffff;
            color: #263241;
            border: 1px solid #d8e0ea;
            border-radius: 10px;
            padding: 8px 18px;
            font-weight: 700;
        }
        QPushButton:hover {
            background: #f8fbff;
            border-color: #a8c3f5;
        }
        QDialogButtonBox QPushButton {
            min-width: 78px;
        }
    )"));
}
} // namespace

const QString kWin7Version = QStringLiteral("1.32");

MainWindow::MainWindow(QWidget* parent)
    : QMainWindow(parent)
{
    setWindowTitle(QStringLiteral("OneSync Win7"));
    resize(1120, 680);
    QApplication::setStyle(QStyleFactory::create(QStringLiteral("Fusion")));

    buildUi();
    setupTray();
    loadTasks();
    refreshLogFilter();
    refreshTaskTable();
    appendLog(QStringLiteral("OneSync Win7 Qt 客户端已启动。"));
}

void MainWindow::buildUi()
{
    auto* root = new QWidget(this);
    root->setObjectName(QStringLiteral("appRoot"));
    auto* shell = new QHBoxLayout(root);
    shell->setContentsMargins(0, 0, 0, 0);
    shell->setSpacing(0);

    auto* sidebar = new QFrame(root);
    sidebar->setObjectName(QStringLiteral("sidebar"));
    sidebar->setFixedWidth(168);
    auto* sidebarLayout = new QVBoxLayout(sidebar);
    sidebarLayout->setContentsMargins(14, 18, 14, 18);
    sidebarLayout->setSpacing(10);

    auto* brand = new QLabel(QStringLiteral("OneSync"));
    brand->setObjectName(QStringLiteral("brand"));
    sidebarLayout->addWidget(brand);

    const QStringList navItems = {
        QStringLiteral("同步任务"),
        QStringLiteral("设备管理"),
        QStringLiteral("连接管理"),
        QStringLiteral("日志"),
        QStringLiteral("关于")
    };
    for (int index = 0; index < navItems.size(); ++index) {
        auto* item = new QPushButton(navItems.at(index));
        item->setObjectName(QStringLiteral("navButton"));
        item->setProperty("active", index == 0);
        item->setMinimumHeight(38);
        item->setCursor(Qt::PointingHandCursor);
        item->setFlat(true);
        item->setCheckable(true);
        item->setChecked(index == 0);
        connect(item, &QPushButton::clicked, this, [this, index]() {
            switchPage(index);
        });
        navButtons.append(item);
        sidebarLayout->addWidget(item);
    }
    sidebarLayout->addStretch(1);

    auto* content = new QWidget(root);
    auto* layout = new QVBoxLayout(content);
    layout->setContentsMargins(22, 20, 22, 20);
    layout->setSpacing(14);

    auto* titleRow = new QHBoxLayout();
    pageTitleLabel = new QLabel(QStringLiteral("同步任务"));
    pageTitleLabel->setObjectName(QStringLiteral("pageTitle"));
    QFont titleFont = pageTitleLabel->font();
    titleFont.setPointSize(titleFont.pointSize() + 5);
    titleFont.setBold(true);
    pageTitleLabel->setFont(titleFont);
    summaryLabel = new QLabel(QStringLiteral("0 个任务"));
    summaryLabel->setAlignment(Qt::AlignRight | Qt::AlignVCenter);
    titleRow->addWidget(pageTitleLabel);
    titleRow->addStretch(1);
    titleRow->addWidget(summaryLabel);
    layout->addLayout(titleRow);

    pages = new QStackedWidget(content);
    pages->setObjectName(QStringLiteral("pages"));
    auto* syncPage = new QWidget();
    auto* syncLayout = new QVBoxLayout(syncPage);
    syncLayout->setContentsMargins(0, 0, 0, 0);
    syncLayout->setSpacing(14);

    auto* toolbar = new QHBoxLayout();
    toolbar->setSpacing(10);
    startButton = toolbarButton(QStringLiteral("开始"));
    pauseButton = toolbarButton(QStringLiteral("暂停"));
    rescanButton = toolbarButton(QStringLiteral("重新扫描"));
    parametersButton = toolbarButton(QStringLiteral("参数"));
    deleteButton = toolbarButton(QStringLiteral("删除"));
    linkButton = toolbarButton(QStringLiteral("查看链接"));
    auto* createButton = toolbarButton(QStringLiteral("+ 创建同步"), QStringLiteral("primaryButton"));
    auto* addButton = toolbarButton(QStringLiteral("加入同步"), QStringLiteral("secondaryButton"));
    auto* copyErrorButton = toolbarButton(QStringLiteral("复制错误详情"));
    auto* selectedDiagnosticsButton = toolbarButton(QStringLiteral("导出选中任务"));
    auto* diagnosticsButton = toolbarButton(QStringLiteral("导出诊断"));

    connect(createButton, &QPushButton::clicked, this, &MainWindow::createTask);
    connect(startButton, &QPushButton::clicked, this, &MainWindow::startSelectedTask);
    connect(pauseButton, &QPushButton::clicked, this, &MainWindow::pauseSelectedTask);
    connect(rescanButton, &QPushButton::clicked, this, &MainWindow::rescanSelectedTask);
    connect(parametersButton, &QPushButton::clicked, this, &MainWindow::editSelectedTask);
    connect(deleteButton, &QPushButton::clicked, this, &MainWindow::deleteSelectedTask);
    connect(linkButton, &QPushButton::clicked, this, &MainWindow::showSelectedSourceLink);
    connect(addButton, &QPushButton::clicked, this, &MainWindow::addTask);
    connect(copyErrorButton, &QPushButton::clicked, this, &MainWindow::copySelectedTaskError);
    connect(selectedDiagnosticsButton, &QPushButton::clicked, this, &MainWindow::exportSelectedTaskDiagnostics);
    connect(diagnosticsButton, &QPushButton::clicked, this, &MainWindow::exportDiagnostics);

    toolbar->addWidget(startButton);
    toolbar->addWidget(pauseButton);
    toolbar->addWidget(rescanButton);
    toolbar->addWidget(parametersButton);
    toolbar->addWidget(deleteButton);
    toolbar->addWidget(linkButton);
    toolbar->addStretch(1);
    toolbar->addWidget(createButton);
    toolbar->addWidget(addButton);
    toolbar->addWidget(copyErrorButton);
    toolbar->addWidget(selectedDiagnosticsButton);
    toolbar->addWidget(diagnosticsButton);
    syncLayout->addLayout(toolbar);

    auto* tableCard = new QFrame();
    tableCard->setObjectName(QStringLiteral("card"));
    auto* tableLayout = new QVBoxLayout(tableCard);
    tableLayout->setContentsMargins(1, 1, 1, 1);
    tableLayout->setSpacing(0);
    taskTable = new QTableWidget(0, ColumnCount, this);
    taskTable->setObjectName(QStringLiteral("taskTable"));
    taskTable->setSelectionBehavior(QAbstractItemView::SelectRows);
    taskTable->setSelectionMode(QAbstractItemView::SingleSelection);
    taskTable->setEditTriggers(QAbstractItemView::NoEditTriggers);
    taskTable->setAlternatingRowColors(true);
    taskTable->setShowGrid(false);
    taskTable->verticalHeader()->setVisible(false);
    taskTable->horizontalHeader()->setStretchLastSection(true);
    taskTable->horizontalHeader()->setSectionResizeMode(QHeaderView::Interactive);
    taskTable->horizontalHeader()->setMinimumHeight(42);
    taskTable->setMinimumHeight(280);
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
    connect(taskTable, &QTableWidget::itemSelectionChanged, this, [this]() {
        refreshButtons();
        if (logFilterCombo != nullptr && logFilterCombo->currentData().toString() == QStringLiteral("__selected__")) {
            rebuildLogView();
        }
    });
    connect(taskTable, &QTableWidget::cellDoubleClicked, this, [this](int, int) {
        editSelectedTask();
    });
    tableLayout->addWidget(taskTable);
    syncLayout->addWidget(tableCard, 3);

    auto* logBox = new QFrame();
    logBox->setObjectName(QStringLiteral("card"));
    auto* logLayout = new QVBoxLayout(logBox);
    logLayout->setContentsMargins(18, 14, 18, 18);
    auto* logTitleRow = new QHBoxLayout();
    auto* logTitle = new QLabel(QStringLiteral("日志"));
    logTitle->setObjectName(QStringLiteral("cardTitle"));
    logTitleRow->addWidget(logTitle);
    logTitleRow->addStretch(1);
    auto* logFilterRow = new QHBoxLayout();
    logFilterRow->addWidget(new QLabel(QStringLiteral("范围")));
    logFilterCombo = new QComboBox();
    connect(logFilterCombo, QOverload<int>::of(&QComboBox::currentIndexChanged), this, &MainWindow::rebuildLogView);
    logFilterRow->addWidget(logFilterCombo);
    auto* copyLogsButton = toolbarButton(QStringLiteral("复制日志"));
    auto* clearLogsButton = toolbarButton(QStringLiteral("清空日志"));
    connect(copyLogsButton, &QPushButton::clicked, this, &MainWindow::copyVisibleLogs);
    connect(clearLogsButton, &QPushButton::clicked, this, &MainWindow::clearVisibleLogs);
    logTitleRow->addLayout(logFilterRow);
    logTitleRow->addWidget(copyLogsButton);
    logTitleRow->addWidget(clearLogsButton);
    logLayout->addLayout(logTitleRow);
    logEdit = new QPlainTextEdit();
    logEdit->setObjectName(QStringLiteral("logEdit"));
    logEdit->setReadOnly(true);
    logEdit->setPlaceholderText(QStringLiteral("运行日志会显示在这里。"));
    logLayout->addWidget(logEdit);
    syncLayout->addWidget(logBox, 2);

    pages->addWidget(syncPage);

    auto* devicePage = new QFrame();
    devicePage->setObjectName(QStringLiteral("card"));
    auto* deviceLayout = new QVBoxLayout(devicePage);
    deviceLayout->setContentsMargins(18, 16, 18, 18);
    auto* deviceToolbar = new QHBoxLayout();
    auto* deviceHint = new QLabel(QStringLiteral("这里汇总每个同步任务已知的设备状态。Win7 版当前按“一个任务对应一个接收端”管理；多台目标机请创建多个接收任务。"));
    deviceHint->setWordWrap(true);
    auto* renameDeviceButton = toolbarButton(QStringLiteral("重命名设备"));
    auto* disableDeviceButton = toolbarButton(QStringLiteral("禁用/启用设备"));
    auto* kickDeviceButton = toolbarButton(QStringLiteral("踢出/重置"));
    connect(renameDeviceButton, &QPushButton::clicked, this, &MainWindow::renameSelectedDevice);
    connect(disableDeviceButton, &QPushButton::clicked, this, &MainWindow::toggleSelectedDeviceDisabled);
    connect(kickDeviceButton, &QPushButton::clicked, this, &MainWindow::kickSelectedDevice);
    deviceToolbar->addWidget(deviceHint, 1);
    deviceToolbar->addWidget(renameDeviceButton);
    deviceToolbar->addWidget(disableDeviceButton);
    deviceToolbar->addWidget(kickDeviceButton);
    deviceLayout->addLayout(deviceToolbar);
    deviceTable = new QTableWidget(0, 7, this);
    deviceTable->setObjectName(QStringLiteral("taskTable"));
    deviceTable->setSelectionBehavior(QAbstractItemView::SelectRows);
    deviceTable->setEditTriggers(QAbstractItemView::NoEditTriggers);
    deviceTable->verticalHeader()->setVisible(false);
    deviceTable->setShowGrid(false);
    deviceTable->setAlternatingRowColors(true);
    deviceTable->setHorizontalHeaderLabels(QStringList()
        << QStringLiteral("任务")
        << QStringLiteral("类型")
        << QStringLiteral("别名")
        << QStringLiteral("设备")
        << QStringLiteral("状态")
        << QStringLiteral("本地目录")
        << QStringLiteral("详情"));
    deviceTable->horizontalHeader()->setStretchLastSection(true);
    connect(deviceTable, &QTableWidget::cellClicked, this, [this](int row, int) {
        if (taskTable != nullptr && row >= 0 && row < tasks.size()) {
            taskTable->selectRow(row);
        }
    });
    deviceLayout->addWidget(deviceTable, 1);
    pages->addWidget(devicePage);

    auto* connectionPage = new QFrame();
    connectionPage->setObjectName(QStringLiteral("card"));
    auto* connectionLayout = new QVBoxLayout(connectionPage);
    connectionLayout->setContentsMargins(18, 16, 18, 18);
    auto* connectionToolbar = new QHBoxLayout();
    auto* connectionHint = new QLabel(QStringLiteral("这里集中查看任务连接方式、源端/Relay 地址和最近状态，并可测试 TLS 连通性。"));
    connectionHint->setWordWrap(true);
    auto* testConnectionButton = toolbarButton(QStringLiteral("测试选中连接"));
    connect(testConnectionButton, &QPushButton::clicked, this, &MainWindow::testSelectedConnection);
    connectionToolbar->addWidget(connectionHint, 1);
    connectionToolbar->addWidget(testConnectionButton);
    connectionLayout->addLayout(connectionToolbar);
    connectionTable = new QTableWidget(0, 6, this);
    connectionTable->setObjectName(QStringLiteral("taskTable"));
    connectionTable->setSelectionBehavior(QAbstractItemView::SelectRows);
    connectionTable->setEditTriggers(QAbstractItemView::NoEditTriggers);
    connectionTable->verticalHeader()->setVisible(false);
    connectionTable->setShowGrid(false);
    connectionTable->setAlternatingRowColors(true);
    connectionTable->setHorizontalHeaderLabels(QStringList()
        << QStringLiteral("任务")
        << QStringLiteral("连接")
        << QStringLiteral("源端地址")
        << QStringLiteral("Relay 地址")
        << QStringLiteral("状态")
        << QStringLiteral("详情"));
    connectionTable->horizontalHeader()->setStretchLastSection(true);
    connect(connectionTable, &QTableWidget::cellClicked, this, [this](int row, int) {
        if (taskTable != nullptr && row >= 0 && row < tasks.size()) {
            taskTable->selectRow(row);
        }
    });
    connectionLayout->addWidget(connectionTable, 1);
    pages->addWidget(connectionPage);

    auto* logsPage = new QFrame();
    logsPage->setObjectName(QStringLiteral("card"));
    auto* logsLayout = new QVBoxLayout(logsPage);
    logsLayout->setContentsMargins(18, 16, 18, 18);
    auto* logsToolbar = new QHBoxLayout();
    auto* logsHint = new QLabel(QStringLiteral("完整运行日志"));
    logsHint->setObjectName(QStringLiteral("cardTitle"));
    auto* copyPageLogsButton = toolbarButton(QStringLiteral("复制日志"));
    auto* clearPageLogsButton = toolbarButton(QStringLiteral("清空日志"));
    auto* exportLogsButton = toolbarButton(QStringLiteral("导出诊断"));
    connect(copyPageLogsButton, &QPushButton::clicked, this, &MainWindow::copyVisibleLogs);
    connect(clearPageLogsButton, &QPushButton::clicked, this, &MainWindow::clearVisibleLogs);
    connect(exportLogsButton, &QPushButton::clicked, this, &MainWindow::exportDiagnostics);
    logsToolbar->addWidget(logsHint);
    logsToolbar->addStretch(1);
    logsToolbar->addWidget(copyPageLogsButton);
    logsToolbar->addWidget(clearPageLogsButton);
    logsToolbar->addWidget(exportLogsButton);
    logsLayout->addLayout(logsToolbar);
    pageLogEdit = new QPlainTextEdit();
    pageLogEdit->setObjectName(QStringLiteral("logEdit"));
    pageLogEdit->setReadOnly(true);
    logsLayout->addWidget(pageLogEdit, 1);
    pages->addWidget(logsPage);

    auto* aboutPage = new QFrame();
    aboutPage->setObjectName(QStringLiteral("card"));
    auto* aboutLayout = new QVBoxLayout(aboutPage);
    aboutLayout->setContentsMargins(26, 24, 26, 24);
    aboutLayout->setSpacing(12);
    auto* aboutTitle = new QLabel(QStringLiteral("OneSync Win7 Qt"));
    aboutTitle->setObjectName(QStringLiteral("pageTitle"));
    auto* aboutText = new QLabel(QStringLiteral(
        "版本：%1\n"
        "定位：Windows 7 兼容客户端\n"
        "能力：创建同步、加入同步、Relay TLS、设备管理、任务日志、诊断导出、托盘运行\n"
        "说明：Win7 源端当前优先使用 Relay；直连监听会在后续版本继续完善。").arg(kWin7Version));
    aboutText->setWordWrap(true);
    aboutLayout->addWidget(aboutTitle);
    aboutLayout->addWidget(aboutText);
    aboutLayout->addStretch(1);
    pages->addWidget(aboutPage);

    layout->addWidget(pages, 1);

    shell->addWidget(sidebar);
    shell->addWidget(content, 1);
    setCentralWidget(root);
    setStyleSheet(QStringLiteral(R"(
        QWidget#appRoot {
            background: #f4f7fb;
            color: #1f2937;
            font-family: "Microsoft YaHei", "Segoe UI", sans-serif;
            font-size: 10.5pt;
        }
        QFrame#sidebar {
            background: #ffffff;
            border-right: 1px solid #e5eaf2;
        }
        QLabel#brand {
            color: #0f172a;
            font-size: 18pt;
            font-weight: 700;
            padding: 4px 4px 14px 4px;
        }
        QPushButton#navButton {
            background: transparent;
            border: none;
            border-radius: 10px;
            padding: 9px 12px;
            color: #475569;
            font-weight: 600;
            text-align: left;
        }
        QPushButton#navButton:hover {
            background: #f1f5f9;
        }
        QPushButton#navButton[active="true"] {
            background: #e8f1ff;
            color: #1d4ed8;
            border-left: 4px solid #2563eb;
        }
        QLabel#pageTitle {
            color: #111827;
            font-size: 19pt;
            font-weight: 700;
        }
        QLabel#cardTitle {
            color: #111827;
            font-size: 12pt;
            font-weight: 700;
        }
        QFrame#card {
            background: #ffffff;
            border: 1px solid #dbe3ef;
            border-radius: 14px;
        }
        QPushButton {
            background: #ffffff;
            color: #263241;
            border: 1px solid #d8e0ea;
            border-radius: 9px;
            padding: 7px 15px;
            font-weight: 600;
        }
        QPushButton:hover {
            background: #f8fbff;
            border-color: #a8c3f5;
        }
        QPushButton:disabled {
            color: #a0a8b3;
            background: #f3f5f8;
        }
        QPushButton#primaryButton {
            background: #2563eb;
            color: white;
            border-color: #2563eb;
        }
        QPushButton#primaryButton:hover {
            background: #1d4ed8;
        }
        QPushButton#secondaryButton {
            background: #f8fbff;
            color: #1d4ed8;
            border-color: #bcd2ff;
        }
        QTableWidget#taskTable {
            background: #ffffff;
            alternate-background-color: #f8fafc;
            border: none;
            border-radius: 14px;
            selection-background-color: #e8f1ff;
            selection-color: #0f172a;
            gridline-color: transparent;
        }
        QHeaderView::section {
            background: #f8fafc;
            color: #475569;
            border: none;
            border-bottom: 1px solid #e5eaf2;
            padding: 9px 8px;
            font-weight: 700;
        }
        QTableWidget::item {
            border-bottom: 1px solid #edf2f7;
            padding: 9px 8px;
        }
        QPlainTextEdit#logEdit {
            background: #0f172a;
            color: #dbeafe;
            border: none;
            border-radius: 10px;
            padding: 10px;
            font-family: Consolas, "Microsoft YaHei", monospace;
        }
        QLineEdit, QTextEdit, QComboBox {
            background: #ffffff;
            border: 1px solid #d8e0ea;
            border-radius: 8px;
            padding: 7px;
        }
        QLineEdit:focus, QTextEdit:focus, QComboBox:focus {
            border-color: #2563eb;
        }
    )"));
    refreshLogFilter();
    refreshButtons();
}

void MainWindow::createTask()
{
    SyncTask task;
    task.id = newTaskID();
    task.role = SyncTask::Source;
    task.name = QStringLiteral("发送任务");
    task.status = QStringLiteral("停止");
    task.detail = QStringLiteral("尚未启动");
    if (!runSourceTaskDialog(&task, false)) {
        return;
    }
    tasks.append(task);
    saveTasks();
    refreshLogFilter();
    refreshTaskTable();
    taskTable->selectRow(tasks.size() - 1);
    appendTaskLog(task.id, QStringLiteral("已创建发送同步任务。"));
    startSelectedTask();
    showSourceLink(task);
}

void MainWindow::addTask()
{
    SyncTask task;
    task.id = newTaskID();
    task.role = SyncTask::Target;
    task.name = QStringLiteral("接收任务");
    task.status = QStringLiteral("停止");
    task.detail = QStringLiteral("尚未启动");
    if (!runTaskDialog(&task, false)) {
        return;
    }
    tasks.append(task);
    saveTasks();
    refreshLogFilter();
    refreshTaskTable();
    taskTable->selectRow(tasks.size() - 1);
    appendTaskLog(task.id, QStringLiteral("已加入同步任务。"));
    startSelectedTask();
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
    if (task->deviceDisabled) {
        QMessageBox::information(this, QStringLiteral("设备已禁用"), QStringLiteral("这个任务的设备已禁用，请到设备管理中启用后再启动。"));
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
    const QFileInfo folderInfo(task->targetFolder);
    if (!folderInfo.exists() || !folderInfo.isDir()) {
        QMessageBox::warning(this, QStringLiteral("目录不可用"), isSourceTask(*task)
            ? QStringLiteral("发送文件夹不存在或不是目录。")
            : QStringLiteral("接收文件夹不存在或不是目录。"));
        return;
    }

    const QString taskID = task->id;
    const bool source = isSourceTask(*task);
    task->running = true;
    task->connectedDevices = 0;
    task->receivedBytes = 0;
    task->sentBytes = 0;
    task->currentReceivedRate = 0;
    task->currentSentRate = 0;
    task->lastTrafficReceivedBytes = 0;
    task->lastTrafficSentBytes = 0;
    task->ignoredCount = 0;
    task->startedAtMs = QDateTime::currentDateTimeUtc().toMSecsSinceEpoch();
    task->lastTrafficAtMs = task->startedAtMs;
    task->status = QStringLiteral("运行-连接中");
    task->detail = source ? QStringLiteral("正在等待目标端") : QStringLiteral("正在连接源端");
    refreshTaskTable();
    appendLog(source
        ? QStringLiteral("[%1] 开始等待目标端。").arg(task->name)
        : QStringLiteral("[%1] 开始连接源端。").arg(task->name));

    QThread* thread = new QThread(this);
    QObject* worker = nullptr;
    TargetConnector* connector = nullptr;
    SourceConnector* sourceConnector = nullptr;
    if (source) {
        sourceConnector = new SourceConnector(task->link, task->targetFolder, task->ignoreRules);
        worker = sourceConnector;
        sourceConnector->moveToThread(thread);
        sourceConnectors.insert(taskID, sourceConnector);
    } else {
        connector = new TargetConnector(task->link, task->targetFolder, task->ignoreRules);
        worker = connector;
        connector->moveToThread(thread);
        connectors.insert(taskID, connector);
    }
    connectionThreads.insert(taskID, thread);

    if (sourceConnector != nullptr) {
        connect(thread, &QThread::started, sourceConnector, &SourceConnector::run);
        connect(sourceConnector, &SourceConnector::logMessage, this, [this, taskID](const QString& message) {
            appendTaskLog(taskID, message);
        });
        connect(sourceConnector, &SourceConnector::statusChanged, this, [this, taskID](const QString& status) {
            setTaskStatus(taskID, status);
        });
        connect(sourceConnector, &SourceConnector::trafficChanged, this, [this, taskID](quint64 receivedBytes, quint64 sentBytes) {
            SyncTask* task = taskByID(taskID);
            if (task == nullptr) {
                return;
            }
            updateTaskTraffic(task, receivedBytes, sentBytes);
            refreshTaskTable();
        });
        connect(sourceConnector, &SourceConnector::fileProgress, this, [this, taskID](const QString& path, quint64 transferredBytes, quint64 totalBytes) {
            SyncTask* task = taskByID(taskID);
            if (task == nullptr) {
                return;
            }
            task->status = QStringLiteral("运行-传输中");
            task->detail = QStringLiteral("正在发送：%1，%2 / %3")
                .arg(path, formatBytes(transferredBytes), formatBytes(totalBytes));
            refreshTaskTable();
        });
        connect(sourceConnector, &SourceConnector::snapshotScanned, this, [this, taskID](quint64 fileCount, quint64 byteCount, quint64 ignoredCount) {
            SyncTask* task = taskByID(taskID);
            if (task == nullptr) {
                return;
            }
            task->localFiles = int(fileCount);
            task->localBytes = byteCount;
            task->globalBytes = byteCount;
            task->ignoredCount = ignoredCount;
            task->detail = QStringLiteral("本地 %1 个文件，忽略 %2 项").arg(fileCount).arg(ignoredCount);
            refreshTaskTable();
        });
        connect(sourceConnector, &SourceConnector::planReceived, this, [this, taskID](int operationCount, quint64 standardBytes) {
            SyncTask* task = taskByID(taskID);
            if (task == nullptr) {
                return;
            }
            task->globalBytes = standardBytes;
            task->detail = QStringLiteral("同步计划：%1 个文件").arg(operationCount);
            refreshTaskTable();
        });
    } else if (connector != nullptr) {
        connect(thread, &QThread::started, connector, &TargetConnector::run);
        connect(connector, &TargetConnector::logMessage, this, [this, taskID](const QString& message) {
            appendTaskLog(taskID, message);
        });
        connect(connector, &TargetConnector::statusChanged, this, [this, taskID](const QString& status) {
            setTaskStatus(taskID, status);
        });
        connect(connector, &TargetConnector::trafficChanged, this, [this, taskID](quint64 receivedBytes, quint64 sentBytes) {
            SyncTask* task = taskByID(taskID);
            if (task == nullptr) {
                return;
            }
            updateTaskTraffic(task, receivedBytes, sentBytes);
            refreshTaskTable();
        });
        connect(connector, &TargetConnector::fileProgress, this, [this, taskID](const QString& path, quint64 transferredBytes, quint64 totalBytes) {
            SyncTask* task = taskByID(taskID);
            if (task == nullptr) {
                return;
            }
            task->status = QStringLiteral("运行-传输中");
            task->detail = QStringLiteral("正在接收：%1，%2 / %3")
                .arg(path, formatBytes(transferredBytes), formatBytes(totalBytes));
            refreshTaskTable();
        });
        connect(connector, &TargetConnector::snapshotScanned, this, [this, taskID](quint64 fileCount, quint64 byteCount, quint64 ignoredCount) {
            SyncTask* task = taskByID(taskID);
            if (task == nullptr) {
                return;
            }
            task->localFiles = int(fileCount);
            task->localBytes = byteCount;
            task->ignoredCount = ignoredCount;
            task->detail = QStringLiteral("本地 %1 个文件，忽略 %2 项").arg(fileCount).arg(ignoredCount);
            refreshTaskTable();
        });
        connect(connector, &TargetConnector::planReceived, this, [this, taskID](int operationCount, quint64 standardBytes) {
            SyncTask* task = taskByID(taskID);
            if (task == nullptr) {
                return;
            }
            task->globalBytes = standardBytes;
            task->detail = QStringLiteral("同步计划：%1 个文件").arg(operationCount);
            refreshTaskTable();
        });
    }

    auto handleFinished = [this, taskID](bool ok, const QString& message) {
        for (SyncTask& item : tasks) {
            if (item.id != taskID) {
                continue;
            }
            item.running = false;
            const bool cancelled = message.contains(QStringLiteral("取消"));
            item.connectedDevices = ok ? 1 : 0;
            item.status = ok ? QStringLiteral("运行-本轮完成") : (cancelled ? QStringLiteral("停止") : QStringLiteral("失败"));
            item.detail = message;
            appendTaskLog(taskID, message);
            break;
        }
        saveTasks();
        refreshTaskTable();
    };

    if (sourceConnector != nullptr) {
        connect(sourceConnector, &SourceConnector::finished, this, handleFinished);
        connect(sourceConnector, &SourceConnector::finished, thread, &QThread::quit);
        connect(sourceConnector, &SourceConnector::finished, sourceConnector, &SourceConnector::deleteLater);
    } else if (connector != nullptr) {
        connect(connector, &TargetConnector::finished, this, handleFinished);
        connect(connector, &TargetConnector::finished, thread, &QThread::quit);
        connect(connector, &TargetConnector::finished, connector, &TargetConnector::deleteLater);
    }

    connect(thread, &QThread::finished, thread, &QThread::deleteLater);
    connect(thread, &QThread::finished, this, [this, taskID]() {
        connectionThreads.remove(taskID);
        connectors.remove(taskID);
        sourceConnectors.remove(taskID);
        refreshButtons();
    });

    if (worker == nullptr) {
        thread->deleteLater();
        return;
    }
    thread->start();
}

void MainWindow::pauseSelectedTask()
{
    SyncTask* task = selectedTask();
    if (task == nullptr) {
        return;
    }
    if (task->running) {
        TargetConnector* connector = connectors.value(task->id, nullptr);
        if (connector != nullptr) {
            connector->cancel();
        }
        SourceConnector* sourceConnector = sourceConnectors.value(task->id, nullptr);
        if (sourceConnector != nullptr) {
            sourceConnector->cancel();
        }
        task->status = QStringLiteral("停止中");
        task->detail = QStringLiteral("正在取消当前同步");
        refreshTaskTable();
        appendTaskLog(task->id, QStringLiteral("已请求暂停当前同步。"));
        return;
    }
    task->status = QStringLiteral("停止");
    task->detail = QStringLiteral("已停止");
    task->connectedDevices = 0;
    saveTasks();
    refreshTaskTable();
    appendTaskLog(task->id, QStringLiteral("已停止。"));
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
    quint64 ignoredCount = 0;
    appendTaskLog(task->id, QStringLiteral("开始重新扫描本地目录。"));
    if (!SnapshotScanner::scanToJson(task->targetFolder, task->ignoreRules, &snapshotJson, &fileCount, &byteCount, &ignoredCount, &error)) {
        task->status = QStringLiteral("扫描失败");
        task->detail = error;
        refreshTaskTable();
        QMessageBox::warning(this, QStringLiteral("扫描失败"), error);
        return;
    }
    task->localFiles = int(fileCount);
    task->localBytes = byteCount;
    task->ignoredCount = ignoredCount;
    if (isSourceTask(*task) || task->globalBytes == 0) {
        task->globalBytes = byteCount;
    }
    task->detail = QStringLiteral("本地 %1 个文件，忽略 %2 项").arg(fileCount).arg(ignoredCount);
    saveTasks();
    refreshTaskTable();
    appendTaskLog(task->id, QStringLiteral("扫描完成：%1 个文件，%2，忽略 %3 项。").arg(fileCount).arg(formatBytes(byteCount)).arg(ignoredCount));
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
    taskLogs.remove(tasks[index].id);
    tasks.removeAt(index);
    saveTasks();
    refreshLogFilter();
    refreshTaskTable();
    appendLog(QStringLiteral("已删除同步任务：%1").arg(name));
}

void MainWindow::showSelectedSourceLink()
{
    SyncTask* task = selectedTask();
    if (task == nullptr) {
        QMessageBox::information(this, QStringLiteral("未选择任务"), QStringLiteral("请先选中一个发送任务。"));
        return;
    }
    if (!isSourceTask(*task)) {
        QMessageBox::information(this, QStringLiteral("不是发送任务"), QStringLiteral("只有发送任务才有同步链接。接收任务请使用源端发来的链接加入。"));
        return;
    }
    if (task->linkText.trimmed().isEmpty()) {
        QMessageBox::warning(this, QStringLiteral("没有同步链接"), QStringLiteral("这个发送任务还没有同步链接，请打开“参数”重新保存并生成链接。"));
        return;
    }
    QString error;
    if (!parseTaskLink(task, &error)) {
        QMessageBox::warning(this, QStringLiteral("同步链接无效"), error);
        return;
    }
    showSourceLink(*task);
}

void MainWindow::renameSelectedDevice()
{
    SyncTask* task = selectedTask();
    if (task == nullptr) {
        QMessageBox::information(this, QStringLiteral("未选择任务"), QStringLiteral("请先在设备管理中选择一个任务。"));
        return;
    }
    bool ok = false;
    const QString current = task->deviceAlias.isEmpty() ? task->name : task->deviceAlias;
    const QString alias = QInputDialog::getText(
        this,
        QStringLiteral("重命名设备"),
        QStringLiteral("设备别名"),
        QLineEdit::Normal,
        current,
        &ok
    ).trimmed();
    if (!ok) {
        return;
    }
    if (alias.isEmpty() || alias.size() > 64) {
        QMessageBox::warning(this, QStringLiteral("别名不可用"), QStringLiteral("设备别名不能为空，且不能超过 64 个字符。"));
        return;
    }
    task->deviceAlias = alias;
    saveTasks();
    refreshTaskTable();
    appendTaskLog(task->id, QStringLiteral("设备别名已改为：%1").arg(alias));
}

void MainWindow::toggleSelectedDeviceDisabled()
{
    SyncTask* task = selectedTask();
    if (task == nullptr) {
        QMessageBox::information(this, QStringLiteral("未选择任务"), QStringLiteral("请先在设备管理中选择一个任务。"));
        return;
    }
    if (task->running) {
        QMessageBox::information(this, QStringLiteral("任务正在运行"), QStringLiteral("请先暂停任务，再禁用或启用设备。"));
        return;
    }
    task->deviceDisabled = !task->deviceDisabled;
    task->detail = task->deviceDisabled ? QStringLiteral("设备已禁用") : QStringLiteral("设备已启用");
    if (task->deviceDisabled) {
        task->connectedDevices = 0;
        task->status = QStringLiteral("停止");
    }
    saveTasks();
    refreshTaskTable();
    appendTaskLog(task->id, task->deviceDisabled ? QStringLiteral("设备已禁用。") : QStringLiteral("设备已启用。"));
}

void MainWindow::kickSelectedDevice()
{
    SyncTask* task = selectedTask();
    if (task == nullptr) {
        QMessageBox::information(this, QStringLiteral("未选择任务"), QStringLiteral("请先在设备管理中选择一个任务。"));
        return;
    }
    if (task->running) {
        QMessageBox::information(this, QStringLiteral("任务正在运行"), QStringLiteral("请先暂停任务，再踢出或重置设备。"));
        return;
    }
    const bool source = isSourceTask(*task);
    const QString prompt = source
        ? QStringLiteral("将清除这个发送任务当前记录的设备状态。同步链接会保留，目标端需要重新加入或重新启动。")
        : QStringLiteral("将清除这个接收任务保存的同步链接和设备状态。之后需要重新粘贴源端链接加入。");
    if (QMessageBox::question(this, QStringLiteral("踢出/重置设备"), prompt) != QMessageBox::Yes) {
        return;
    }

    task->connectedDevices = 0;
    task->deviceAlias.clear();
    task->deviceDisabled = false;
    task->status = QStringLiteral("停止");
    if (source) {
        task->detail = QStringLiteral("已重置设备状态，等待目标端重新加入。");
    } else {
        task->linkText.clear();
        task->link = SyncLink();
        task->linkReady = false;
        task->detail = QStringLiteral("已清除同步链接，请重新加入。");
    }

    saveTasks();
    refreshLogFilter();
    refreshTaskTable();
    appendTaskLog(task->id, source
        ? QStringLiteral("已重置设备状态，同步链接已保留。")
        : QStringLiteral("已踢出设备并清除接收端同步链接。"));
}

void MainWindow::testSelectedConnection()
{
    SyncTask* task = selectedTask();
    if (task == nullptr) {
        QMessageBox::information(this, QStringLiteral("未选择任务"), QStringLiteral("请先在连接管理中选择一个任务。"));
        return;
    }
    QString detail;
    appendTaskLog(task->id, QStringLiteral("开始测试连接。"));
    const bool ok = testTaskConnection(*task, &detail);
    appendTaskLog(task->id, detail);
    QMessageBox::information(
        this,
        ok ? QStringLiteral("连接测试通过") : QStringLiteral("连接测试失败"),
        detail
    );
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

void MainWindow::exportSelectedTaskDiagnostics()
{
    const SyncTask* task = selectedTask();
    if (task == nullptr) {
        QMessageBox::information(this, QStringLiteral("未选择任务"), QStringLiteral("请先选中一个同步任务。"));
        return;
    }
    const QString safeName = task->name.simplified().replace(QLatin1Char(' '), QLatin1Char('_'));
    const QString defaultDir = QStandardPaths::writableLocation(QStandardPaths::DesktopLocation);
    const QString fileName = QFileDialog::getSaveFileName(
        this,
        QStringLiteral("保存选中任务诊断日志"),
        defaultDir + QStringLiteral("/onesync-win7-task-%1-diagnostics.txt").arg(safeName),
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
    QString text;
    text += QStringLiteral("OneSync Win7 Qt 单任务诊断日志\n");
    text += QStringLiteral("生成时间: %1\n\n").arg(QDateTime::currentDateTimeUtc().toString(Qt::ISODate));
    text += taskDiagnosticsText(*task);
    text += QStringLiteral("任务日志:\n");
    text += taskLogs.value(task->id).join(QStringLiteral("\n"));
    text += QStringLiteral("\n");
    file.write(text.toUtf8());
    appendTaskLog(task->id, QStringLiteral("选中任务诊断日志已保存：%1").arg(fileName));
}

void MainWindow::copySelectedTaskError()
{
    const SyncTask* task = selectedTask();
    if (task == nullptr) {
        QMessageBox::information(this, QStringLiteral("未选择任务"), QStringLiteral("请先选中一个同步任务。"));
        return;
    }
    QString text;
    text += QStringLiteral("OneSync Win7 Qt 错误详情\n");
    text += QStringLiteral("生成时间: %1\n\n").arg(QDateTime::currentDateTimeUtc().toString(Qt::ISODate));
    text += taskDiagnosticsText(*task);
    text += QStringLiteral("任务日志:\n");
    text += taskLogs.value(task->id).join(QStringLiteral("\n"));
    text += QStringLiteral("\n");
    QApplication::clipboard()->setText(text);
    appendTaskLog(task->id, QStringLiteral("已复制错误详情。"));
    QMessageBox::information(this, QStringLiteral("已复制"), QStringLiteral("选中任务的错误详情和任务日志已复制。"));
}

void MainWindow::copyVisibleLogs()
{
    QString text;
    if (pages != nullptr && pages->currentIndex() == 3) {
        text = globalLogs.join(QStringLiteral("\n"));
    } else if (logEdit != nullptr) {
        text = logEdit->toPlainText();
    }
    if (text.trimmed().isEmpty()) {
        QMessageBox::information(this, QStringLiteral("暂无日志"), QStringLiteral("当前范围没有可复制的日志。"));
        return;
    }
    QApplication::clipboard()->setText(text);
    QMessageBox::information(this, QStringLiteral("已复制"), QStringLiteral("当前显示的日志已复制。"));
}

void MainWindow::clearVisibleLogs()
{
    if (pages != nullptr && pages->currentIndex() == 3) {
        if (QMessageBox::question(this, QStringLiteral("清空日志"), QStringLiteral("确定清空全部运行日志吗？")) != QMessageBox::Yes) {
            return;
        }
        globalLogs.clear();
        taskLogs.clear();
        rebuildLogView();
        return;
    }

    if (logFilterCombo == nullptr) {
        return;
    }
    const QString filter = logFilterCombo->currentData().toString();
    if (filter == QStringLiteral("__all__")) {
        if (QMessageBox::question(this, QStringLiteral("清空日志"), QStringLiteral("确定清空全部运行日志吗？")) != QMessageBox::Yes) {
            return;
        }
        globalLogs.clear();
        taskLogs.clear();
    } else {
        QString taskID = filter;
        if (filter == QStringLiteral("__selected__")) {
            const SyncTask* task = selectedTask();
            if (task == nullptr) {
                QMessageBox::information(this, QStringLiteral("未选择任务"), QStringLiteral("请先选中一个同步任务。"));
                return;
            }
            taskID = task->id;
        }
        const SyncTask* task = taskByID(taskID);
        const QString taskName = task != nullptr ? task->name : taskID;
        if (QMessageBox::question(this, QStringLiteral("清空日志"), QStringLiteral("确定清空“%1”的任务日志吗？").arg(taskName)) != QMessageBox::Yes) {
            return;
        }
        const QStringList lines = taskLogs.value(taskID);
        for (const QString& line : lines) {
            globalLogs.removeAll(line);
        }
        taskLogs.remove(taskID);
    }
    rebuildLogView();
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
        task.role = settings.value(QStringLiteral("role")).toString() == QStringLiteral("source") ? SyncTask::Source : SyncTask::Target;
        task.name = settings.value(QStringLiteral("name"), QStringLiteral("接收任务")).toString();
        task.deviceAlias = settings.value(QStringLiteral("deviceAlias")).toString();
        task.deviceDisabled = settings.value(QStringLiteral("deviceDisabled")).toBool();
        task.linkText = settings.value(QStringLiteral("link")).toString();
        task.targetFolder = settings.value(QStringLiteral("targetFolder")).toString();
        task.ignoreRules = settings.value(QStringLiteral("ignoreRules")).toStringList();
        task.localBytes = settings.value(QStringLiteral("localBytes")).toULongLong();
        task.globalBytes = settings.value(QStringLiteral("globalBytes")).toULongLong();
        task.ignoredCount = settings.value(QStringLiteral("ignoredCount")).toULongLong();
        task.localFiles = settings.value(QStringLiteral("localFiles")).toInt();
        task.status = QStringLiteral("停止");
        task.detail = QStringLiteral("尚未启动");
        QString error;
        if (!parseTaskLink(&task, &error)) {
            task.status = QStringLiteral("链接无效");
            task.detail = error;
        }
        if (task.deviceDisabled) {
            task.status = QStringLiteral("停止");
            task.detail = QStringLiteral("设备已禁用");
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
        settings.setValue(QStringLiteral("role"), task.role == SyncTask::Source ? QStringLiteral("source") : QStringLiteral("target"));
        settings.setValue(QStringLiteral("name"), task.name);
        settings.setValue(QStringLiteral("deviceAlias"), task.deviceAlias);
        settings.setValue(QStringLiteral("deviceDisabled"), task.deviceDisabled);
        settings.setValue(QStringLiteral("link"), task.linkText);
        settings.setValue(QStringLiteral("targetFolder"), task.targetFolder);
        settings.setValue(QStringLiteral("ignoreRules"), task.ignoreRules);
        settings.setValue(QStringLiteral("localBytes"), task.localBytes);
        settings.setValue(QStringLiteral("globalBytes"), task.globalBytes);
        settings.setValue(QStringLiteral("ignoredCount"), task.ignoredCount);
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
        const QString devices = task.deviceDisabled
            ? QStringLiteral("禁用")
            : QStringLiteral("%1 / %2").arg(task.connectedDevices).arg(task.totalDevices);
        const QStringList values = {
            roleLabel(task),
            task.name,
            statusLabel(task),
            devices,
            task.localBytes > 0 ? formatBytes(task.localBytes) : QStringLiteral("-"),
            task.globalBytes > 0 ? formatBytes(task.globalBytes) : QStringLiteral("-"),
            formatRate(task.currentReceivedRate),
            formatRate(task.currentSentRate)
        };
        for (int column = 0; column < values.size(); ++column) {
            auto* item = taskTable->item(row, column);
            if (item == nullptr) {
                item = new QTableWidgetItem();
                taskTable->setItem(row, column, item);
            }
            item->setText(values[column]);
            item->setToolTip(QStringLiteral("%1\n设备别名：%2\n接收总量：%3\n发送总量：%4\n当前接收：%5\n当前发送：%6\n忽略：%7 项")
                .arg(task.detail,
                    task.deviceAlias.isEmpty() ? QStringLiteral("-") : task.deviceAlias,
                    formatBytes(task.receivedBytes),
                    formatBytes(task.sentBytes),
                    formatRate(task.currentReceivedRate),
                    formatRate(task.currentSentRate))
                .arg(task.ignoredCount));
        }
    }
    summaryLabel->setText(QStringLiteral("%1 个任务").arg(tasks.size()));
    if (selected >= 0 && selected < tasks.size()) {
        taskTable->selectRow(selected);
    }
    refreshSecondaryPages();
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
    linkButton->setEnabled(hasSelection && isSourceTask(*task) && !task->linkText.trimmed().isEmpty());
}

void MainWindow::refreshLogFilter()
{
    if (logFilterCombo == nullptr) {
        return;
    }
    const QString current = logFilterCombo->currentData().toString();
    logFilterCombo->blockSignals(true);
    logFilterCombo->clear();
    logFilterCombo->addItem(QStringLiteral("全部日志"), QStringLiteral("__all__"));
    logFilterCombo->addItem(QStringLiteral("选中任务"), QStringLiteral("__selected__"));
    for (const SyncTask& task : tasks) {
        logFilterCombo->addItem(task.name, task.id);
    }
    const int index = logFilterCombo->findData(current);
    logFilterCombo->setCurrentIndex(index >= 0 ? index : 0);
    logFilterCombo->blockSignals(false);
    rebuildLogView();
}

void MainWindow::rebuildLogView()
{
    if (logEdit == nullptr || logFilterCombo == nullptr) {
        return;
    }
    const QString filter = logFilterCombo->currentData().toString();
    QStringList lines;
    if (filter == QStringLiteral("__all__")) {
        lines = globalLogs;
    } else if (filter == QStringLiteral("__selected__")) {
        const SyncTask* task = selectedTask();
        if (task != nullptr) {
            lines = taskLogs.value(task->id);
        }
    } else {
        lines = taskLogs.value(filter);
    }
    logEdit->setPlainText(lines.join(QStringLiteral("\n")));
    QTextCursor cursor = logEdit->textCursor();
    cursor.movePosition(QTextCursor::End);
    logEdit->setTextCursor(cursor);
    if (pageLogEdit != nullptr) {
        pageLogEdit->setPlainText(globalLogs.join(QStringLiteral("\n")));
        QTextCursor pageCursor = pageLogEdit->textCursor();
        pageCursor.movePosition(QTextCursor::End);
        pageLogEdit->setTextCursor(pageCursor);
    }
}

void MainWindow::switchPage(int page)
{
    if (pages == nullptr || page < 0 || page >= pages->count()) {
        return;
    }
    pages->setCurrentIndex(page);
    const QStringList titles = {
        QStringLiteral("同步任务"),
        QStringLiteral("设备管理"),
        QStringLiteral("连接管理"),
        QStringLiteral("日志"),
        QStringLiteral("关于")
    };
    if (pageTitleLabel != nullptr && page < titles.size()) {
        pageTitleLabel->setText(titles.at(page));
    }
    if (summaryLabel != nullptr) {
        summaryLabel->setVisible(page == 0);
    }
    for (int index = 0; index < navButtons.size(); ++index) {
        const bool active = index == page;
        navButtons[index]->setChecked(active);
        navButtons[index]->setProperty("active", active);
        navButtons[index]->style()->unpolish(navButtons[index]);
        navButtons[index]->style()->polish(navButtons[index]);
    }
    refreshSecondaryPages();
}

void MainWindow::refreshSecondaryPages()
{
    if (deviceTable != nullptr) {
        deviceTable->setRowCount(tasks.size());
        for (int row = 0; row < tasks.size(); ++row) {
            const SyncTask& task = tasks[row];
            const QString device = task.deviceDisabled
                ? QStringLiteral("禁用")
                : QStringLiteral("%1 / %2").arg(task.connectedDevices).arg(task.totalDevices);
            const QStringList values = {
                task.name,
                roleLabel(task),
                task.deviceAlias.isEmpty() ? QStringLiteral("-") : task.deviceAlias,
                device,
                statusLabel(task),
                task.targetFolder,
                task.detail
            };
            for (int column = 0; column < values.size(); ++column) {
                auto* item = deviceTable->item(row, column);
                if (item == nullptr) {
                    item = new QTableWidgetItem();
                    deviceTable->setItem(row, column, item);
                }
                item->setText(values.at(column));
                item->setToolTip(values.at(column));
            }
        }
    }

    if (connectionTable != nullptr) {
        connectionTable->setRowCount(tasks.size());
        for (int row = 0; row < tasks.size(); ++row) {
            const SyncTask& task = tasks[row];
            const QString connection = task.linkReady
                ? (task.link.hasRelay() ? QStringLiteral("Relay TLS") : QStringLiteral("直连 TLS"))
                : QStringLiteral("-");
            const QStringList values = {
                task.name,
                connection,
                task.linkReady ? task.link.endpoint : QStringLiteral("-"),
                task.linkReady && task.link.hasRelay() ? task.link.relayEndpoint : QStringLiteral("-"),
                statusLabel(task),
                task.detail
            };
            for (int column = 0; column < values.size(); ++column) {
                auto* item = connectionTable->item(row, column);
                if (item == nullptr) {
                    item = new QTableWidgetItem();
                    connectionTable->setItem(row, column, item);
                }
                item->setText(values.at(column));
                item->setToolTip(values.at(column));
            }
        }
    }

    if (pageLogEdit != nullptr) {
        pageLogEdit->setPlainText(globalLogs.join(QStringLiteral("\n")));
        QTextCursor cursor = pageLogEdit->textCursor();
        cursor.movePosition(QTextCursor::End);
        pageLogEdit->setTextCursor(cursor);
    }
}

int MainWindow::selectedTaskIndex() const
{
    return selectedTaskIndexFromTables();
}

int MainWindow::selectedTaskIndexFromTables() const
{
    if (taskTable == nullptr) {
        return -1;
    }
    const QList<QTableWidgetItem*> selected = taskTable->selectedItems();
    if (!selected.isEmpty()) {
        const int row = selected.first()->row();
        return row >= 0 && row < tasks.size() ? row : -1;
    }
    if (deviceTable != nullptr) {
        const QList<QTableWidgetItem*> deviceSelected = deviceTable->selectedItems();
        if (!deviceSelected.isEmpty()) {
            const int row = deviceSelected.first()->row();
            return row >= 0 && row < tasks.size() ? row : -1;
        }
    }
    if (connectionTable != nullptr) {
        const QList<QTableWidgetItem*> connectionSelected = connectionTable->selectedItems();
        if (!connectionSelected.isEmpty()) {
            const int row = connectionSelected.first()->row();
            return row >= 0 && row < tasks.size() ? row : -1;
        }
    }
    return -1;
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

MainWindow::SyncTask* MainWindow::taskByID(const QString& taskID)
{
    for (SyncTask& task : tasks) {
        if (task.id == taskID) {
            return &task;
        }
    }
    return nullptr;
}

const MainWindow::SyncTask* MainWindow::taskByID(const QString& taskID) const
{
    for (const SyncTask& task : tasks) {
        if (task.id == taskID) {
            return &task;
        }
    }
    return nullptr;
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
        if (status == QStringLiteral("运行-已连接源端") || status == QStringLiteral("运行-已连接目标端")) {
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

QString MainWindow::roleLabel(const SyncTask& task) const
{
    return task.role == SyncTask::Source ? QStringLiteral("发送") : QStringLiteral("接收");
}

QString MainWindow::statusLabel(const SyncTask& task) const
{
    if (task.deviceDisabled) {
        return QStringLiteral("已禁用");
    }
    if (!task.running) {
        if (task.status == QStringLiteral("运行-本轮完成")) {
            return QStringLiteral("同步完成");
        }
        if (task.status == QStringLiteral("失败") || task.status == QStringLiteral("扫描失败") || task.status == QStringLiteral("链接无效")) {
            return task.status;
        }
        if (task.status == QStringLiteral("停止中")) {
            return QStringLiteral("停止中");
        }
        return QStringLiteral("停止");
    }
    if (task.status == QStringLiteral("运行-连接中")) {
        return isSourceTask(task) ? QStringLiteral("运行-等待目标端") : QStringLiteral("运行-连接源端");
    }
    if (task.status == QStringLiteral("运行-传输中")) {
        return QStringLiteral("运行-传输中");
    }
    if (task.status == QStringLiteral("运行-已连接源端")) {
        return QStringLiteral("运行-已连接源端");
    }
    if (task.status == QStringLiteral("运行-已连接目标端")) {
        return QStringLiteral("运行-已连接目标端");
    }
    if (task.status.contains(QStringLiteral("计划")) || task.detail.contains(QStringLiteral("同步计划"))) {
        return QStringLiteral("运行-同步中");
    }
    return task.status.isEmpty() ? QStringLiteral("运行中") : task.status;
}

bool MainWindow::isSourceTask(const SyncTask& task) const
{
    return task.role == SyncTask::Source;
}

QString MainWindow::buildSourceLink(const QString& relayEndpoint, const QString& relayToken, const QString& caCertificatePem, QString* error) const
{
    Endpoint endpoint;
    if (!EndpointParser::parse(relayEndpoint, &endpoint, error)) {
        return {};
    }
    if (error != nullptr) {
        error->clear();
    }
    QString trustedCertificatePem = caCertificatePem.trimmed();
    const QDateTime issuedAt = QDateTime::currentDateTimeUtc();
    const QDateTime expiresAt = issuedAt.addSecs(24 * 60 * 60);
    QJsonObject object;
    object.insert(QStringLiteral("version"), 1);
    object.insert(QStringLiteral("session_id"), newTaskID());
    object.insert(QStringLiteral("endpoint"), QStringLiteral("127.0.0.1:0"));
    object.insert(QStringLiteral("relay_endpoint"), endpoint.display());
    if (!relayToken.trimmed().isEmpty()) {
        object.insert(QStringLiteral("relay_token"), relayToken.trimmed());
    }
    if (!trustedCertificatePem.isEmpty()) {
        object.insert(QStringLiteral("ca_certificate_pem"), trustedCertificatePem);
    }
    object.insert(QStringLiteral("token"), base64Url(randomBytes(32)));
    object.insert(QStringLiteral("issued_at"), issuedAt.toString(Qt::ISODateWithMs));
    object.insert(QStringLiteral("expires_at"), expiresAt.toString(Qt::ISODateWithMs));
    const QByteArray json = QJsonDocument(object).toJson(QJsonDocument::Compact);
    return base64Url(json);
}

bool MainWindow::testTaskConnection(const SyncTask& task, QString* detail) const
{
    SyncLink link = task.link;
    if (!task.linkReady) {
        QString error;
        if (!SyncLinkParser::parse(task.linkText, &link, &error)) {
            if (detail != nullptr) {
                *detail = QStringLiteral("连接测试失败：同步链接无效：%1").arg(error);
            }
            return false;
        }
    }

    const QString endpointText = link.hasRelay() ? link.relayEndpoint : link.endpoint;
    Endpoint endpoint;
    QString error;
    if (!EndpointParser::parse(endpointText, &endpoint, &error)) {
        if (detail != nullptr) {
            *detail = QStringLiteral("连接测试失败：地址格式错误：%1").arg(error);
        }
        return false;
    }

    QSslSocket socket;
    const QList<QSslCertificate> certificates = QSslCertificate::fromData(link.caCertificatePem.toUtf8(), QSsl::Pem);
    if (!certificates.isEmpty()) {
        socket.setCaCertificates(certificates);
        socket.setPeerVerifyMode(QSslSocket::QueryPeer);
    } else {
        socket.setPeerVerifyMode(QSslSocket::VerifyPeer);
    }
    socket.setPeerVerifyName(endpoint.host);
    socket.connectToHostEncrypted(endpoint.host, endpoint.port);
    if (!socket.waitForEncrypted(8000)) {
        if (detail != nullptr) {
            *detail = QStringLiteral("连接测试失败：%1 %2。地址：%3")
                .arg(link.hasRelay() ? QStringLiteral("Relay TLS") : QStringLiteral("直连 TLS"),
                    socket.errorString(),
                    endpoint.display());
        }
        return false;
    }
    if (!certificates.isEmpty() && !certificateMatchesPinned(socket.peerCertificate(), certificates)) {
        if (detail != nullptr) {
            *detail = QStringLiteral("连接测试失败：Relay 返回的证书和同步链接里的 Relay 证书不一致。地址：%1").arg(endpoint.display());
        }
        return false;
    }
    socket.disconnectFromHost();
    if (detail != nullptr) {
        *detail = certificates.isEmpty()
            ? QStringLiteral("连接测试通过：%1 握手成功。地址：%2")
                .arg(link.hasRelay() ? QStringLiteral("Relay TLS") : QStringLiteral("直连 TLS"),
                    endpoint.display())
            : QStringLiteral("连接测试通过：%1 握手成功，证书指纹已匹配。地址：%2")
                .arg(link.hasRelay() ? QStringLiteral("Relay TLS") : QStringLiteral("直连 TLS"),
                    endpoint.display());
    }
    return true;
}

bool MainWindow::runTaskDialog(SyncTask* task, bool editing)
{
    QDialog dialog(this);
    dialog.setWindowTitle(editing ? QStringLiteral("任务参数") : QStringLiteral("加入同步"));
    applyModernDialogStyle(&dialog);
    auto* layout = new QVBoxLayout(&dialog);
    layout->setContentsMargins(22, 20, 22, 18);
    layout->setSpacing(14);
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
    task->role = SyncTask::Target;
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

bool MainWindow::runSourceTaskDialog(SyncTask* task, bool editing)
{
    QDialog dialog(this);
    dialog.setWindowTitle(editing ? QStringLiteral("发送任务参数") : QStringLiteral("创建同步"));
    applyModernDialogStyle(&dialog);
    auto* layout = new QVBoxLayout(&dialog);
    layout->setContentsMargins(22, 20, 22, 18);
    layout->setSpacing(14);
    auto* form = new QFormLayout();
    auto* nameEdit = new QLineEdit(task->name);
    auto* folderEdit = new QLineEdit(task->targetFolder);
    auto* relayEdit = new QLineEdit(task->linkReady ? task->link.relayEndpoint : QString());
    auto* relayTokenEdit = new QLineEdit(task->linkReady ? task->link.relayToken : QString());
    auto* caEdit = new QTextEdit(task->linkReady ? task->link.caCertificatePem : QString());
    caEdit->setAcceptRichText(false);
    caEdit->setPlaceholderText(QStringLiteral("公网 SSL / 通配符证书请留空。只有自签 Relay 证书才粘贴 sudo onesync-relayctl cert 输出的 PEM。"));

    auto* folderRow = new QHBoxLayout();
    auto* chooseButton = new QPushButton(QStringLiteral("选择目录"));
    folderRow->addWidget(folderEdit);
    folderRow->addWidget(chooseButton);
    connect(chooseButton, &QPushButton::clicked, &dialog, [&dialog, folderEdit]() {
        const QString folder = QFileDialog::getExistingDirectory(&dialog, QStringLiteral("选择发送文件夹"), folderEdit->text());
        if (!folder.isEmpty()) {
            folderEdit->setText(folder);
        }
    });

    form->addRow(QStringLiteral("名称"), nameEdit);
    form->addRow(QStringLiteral("发送目录"), folderRow);
    form->addRow(QStringLiteral("Relay 地址"), relayEdit);
    form->addRow(QStringLiteral("Relay 令牌"), relayTokenEdit);
    form->addRow(QStringLiteral("Relay 证书（可选）"), caEdit);
    layout->addLayout(form);

    auto* hint = new QLabel(QStringLiteral("Win7 源端第一版走 Relay：创建后会生成一段同步链接，目标端只需要粘贴这段链接加入。Relay 使用公网 SSL 或通配符证书时，Relay 证书留空；自签证书才需要粘贴。"));
    hint->setWordWrap(true);
    layout->addWidget(hint);

    auto* buttons = new QDialogButtonBox(QDialogButtonBox::Ok | QDialogButtonBox::Cancel);
    buttons->button(QDialogButtonBox::Ok)->setText(editing ? QStringLiteral("保存并重新生成链接") : QStringLiteral("创建"));
    buttons->button(QDialogButtonBox::Cancel)->setText(QStringLiteral("取消"));
    layout->addWidget(buttons);

    connect(buttons, &QDialogButtonBox::accepted, &dialog, &QDialog::accept);
    connect(buttons, &QDialogButtonBox::rejected, &dialog, &QDialog::reject);

    if (dialog.exec() != QDialog::Accepted) {
        return false;
    }

    const QString name = nameEdit->text().trimmed();
    const QString folder = folderEdit->text().trimmed();
    const QString relayEndpoint = relayEdit->text().trimmed();
    const QString relayToken = relayTokenEdit->text().trimmed();
    const QString caCertificatePem = caEdit->toPlainText().trimmed();
    if (name.isEmpty()) {
        QMessageBox::warning(this, QStringLiteral("名称为空"), QStringLiteral("请填写任务名称。"));
        return false;
    }
    if (folder.isEmpty() || !QFileInfo(folder).isDir()) {
        QMessageBox::warning(this, QStringLiteral("目录不可用"), QStringLiteral("请选择已经存在的发送文件夹。"));
        return false;
    }
    if (relayEndpoint.isEmpty()) {
        QMessageBox::warning(this, QStringLiteral("Relay 为空"), QStringLiteral("Win7 源端当前请填写 Relay 地址，例如 r.example.com:17443。"));
        return false;
    }

    QString error;
    const QString link = buildSourceLink(relayEndpoint, relayToken, caCertificatePem, &error);
    if (link.isEmpty()) {
        QMessageBox::warning(this, QStringLiteral("Relay 地址不可用"), error);
        return false;
    }

    task->role = SyncTask::Source;
    task->name = name;
    task->targetFolder = folder;
    task->linkText = link;
    if (!parseTaskLink(task, &error)) {
        QMessageBox::warning(this, QStringLiteral("同步链接生成失败"), error);
        return false;
    }
    task->status = QStringLiteral("停止");
    task->detail = QStringLiteral("Relay：%1").arg(task->link.relayEndpoint);
    return true;
}

void MainWindow::showSourceLink(const SyncTask& task)
{
    QDialog dialog(this);
    dialog.setWindowTitle(QStringLiteral("同步链接"));
    applyModernDialogStyle(&dialog);
    auto* layout = new QVBoxLayout(&dialog);
    layout->setContentsMargins(22, 20, 22, 18);
    layout->setSpacing(14);
    auto* hint = new QLabel(QStringLiteral("把下面这段同步链接发给目标端，在目标端点击“加入同步”后粘贴即可。这个链接会保存在发送任务里，之后选中任务点“查看链接”可以再次打开。"));
    hint->setWordWrap(true);
    layout->addWidget(hint);
    auto* linkEdit = new QTextEdit(task.linkText);
    linkEdit->setAcceptRichText(false);
    linkEdit->setReadOnly(true);
    linkEdit->setMinimumSize(560, 180);
    layout->addWidget(linkEdit);
    auto* buttons = new QDialogButtonBox(QDialogButtonBox::Close);
    auto* copyButton = buttons->addButton(QStringLiteral("复制链接"), QDialogButtonBox::ActionRole);
    buttons->button(QDialogButtonBox::Close)->setText(QStringLiteral("关闭"));
    connect(copyButton, &QPushButton::clicked, &dialog, [linkEdit]() {
        QApplication::clipboard()->setText(linkEdit->toPlainText());
    });
    connect(buttons, &QDialogButtonBox::rejected, &dialog, &QDialog::reject);
    layout->addWidget(buttons);
    dialog.exec();
}

void MainWindow::showTaskParameters(SyncTask* task)
{
    if (task == nullptr) {
        return;
    }
    QDialog dialog(this);
    dialog.setWindowTitle(QStringLiteral("任务参数"));
    applyModernDialogStyle(&dialog);
    auto* layout = new QVBoxLayout(&dialog);
    layout->setContentsMargins(22, 20, 22, 18);
    layout->setSpacing(14);

    auto* editButton = new QPushButton(isSourceTask(*task)
        ? QStringLiteral("修改名称、目录和 Relay")
        : QStringLiteral("修改名称、目录和链接"));
    layout->addWidget(editButton);
    connect(editButton, &QPushButton::clicked, &dialog, [this, task]() {
        const bool ok = isSourceTask(*task) ? runSourceTaskDialog(task, true) : runTaskDialog(task, true);
        if (ok) {
            saveTasks();
            refreshLogFilter();
            refreshTaskTable();
            appendTaskLog(task->id, QStringLiteral("任务参数已更新。"));
            if (isSourceTask(*task)) {
                showSourceLink(*task);
            }
        }
    });

    if (isSourceTask(*task)) {
        auto* copyLinkButton = new QPushButton(QStringLiteral("复制同步链接"));
        layout->addWidget(copyLinkButton);
        connect(copyLinkButton, &QPushButton::clicked, &dialog, [this, task]() {
            showSourceLink(*task);
        });
    }

    auto* ignoreLabel = new QLabel(QStringLiteral("忽略规则（每行一条，当前先保存规则；后续会接入扫描和同步过滤）："));
    ignoreLabel->setWordWrap(true);
    layout->addWidget(ignoreLabel);
    auto* ignoreEdit = new QTextEdit(task->ignoreRules.join(QStringLiteral("\n")));
    ignoreEdit->setAcceptRichText(false);
    ignoreEdit->setPlaceholderText(QStringLiteral("例如：\n*.tmp\n.cache/\nnode_modules/"));
    layout->addWidget(ignoreEdit, 1);

    auto* templateRow = new QHBoxLayout();
    auto* templateCombo = new QComboBox();
    templateCombo->addItem(QStringLiteral("常见临时文件"), QStringLiteral("*.tmp\n*.temp\n*.bak\n~$*"));
    templateCombo->addItem(QStringLiteral("开发目录"), QStringLiteral("node_modules/\ndist/\nbuild/\n.git/\n.cache/"));
    templateCombo->addItem(QStringLiteral("系统隐藏文件"), QStringLiteral(".DS_Store\nThumbs.db\ndesktop.ini"));
    auto* applyTemplateButton = new QPushButton(QStringLiteral("追加模板"));
    templateRow->addWidget(new QLabel(QStringLiteral("默认模板")));
    templateRow->addWidget(templateCombo, 1);
    templateRow->addWidget(applyTemplateButton);
    layout->addLayout(templateRow);
    connect(applyTemplateButton, &QPushButton::clicked, &dialog, [templateCombo, ignoreEdit]() {
        const QString addition = templateCombo->currentData().toString();
        QString current = ignoreEdit->toPlainText().trimmed();
        if (!current.isEmpty()) {
            current += QStringLiteral("\n");
        }
        ignoreEdit->setPlainText(current + addition);
    });

    auto* testRow = new QHBoxLayout();
    auto* samplePathEdit = new QLineEdit();
    samplePathEdit->setPlaceholderText(QStringLiteral("测试路径，例如 cache/a.tmp 或 logs/debug.txt"));
    auto* sampleDirectoryCombo = new QComboBox();
    sampleDirectoryCombo->addItem(QStringLiteral("文件"), false);
    sampleDirectoryCombo->addItem(QStringLiteral("目录"), true);
    auto* testButton = new QPushButton(QStringLiteral("测试规则"));
    testRow->addWidget(samplePathEdit, 1);
    testRow->addWidget(sampleDirectoryCombo);
    testRow->addWidget(testButton);
    layout->addLayout(testRow);
    auto* testResult = new QLabel(QStringLiteral("输入路径后可测试是否会被忽略。"));
    testResult->setWordWrap(true);
    layout->addWidget(testResult);
    connect(testButton, &QPushButton::clicked, &dialog, [ignoreEdit, samplePathEdit, sampleDirectoryCombo, testResult]() {
        QStringList rules;
        const QStringList lines = ignoreEdit->toPlainText().split(QRegExp(QStringLiteral("[\r\n]+")), QString::SkipEmptyParts);
        for (const QString& line : lines) {
            const QString rule = line.trimmed();
            if (!rule.isEmpty()) {
                rules.append(rule);
            }
        }
        const QString sample = samplePathEdit->text().trimmed();
        if (sample.isEmpty()) {
            testResult->setText(QStringLiteral("请先输入要测试的相对路径。"));
            return;
        }
        IgnoreMatcher matcher(rules);
        const bool directory = sampleDirectoryCombo->currentData().toBool();
        testResult->setText(matcher.matches(sample, directory)
            ? QStringLiteral("会被忽略：%1").arg(sample)
            : QStringLiteral("不会被忽略：%1").arg(sample));
    });

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
    appendTaskLog(task->id, QStringLiteral("忽略规则已保存：%1 条。").arg(rules.size()));
}

QString MainWindow::taskDiagnosticsText(const SyncTask& task) const
{
    QString text;
    text += QStringLiteral("任务: %1\n").arg(task.name);
    text += QStringLiteral("类型: %1\n").arg(roleLabel(task));
    text += QStringLiteral("设备别名: %1\n").arg(task.deviceAlias.isEmpty() ? QStringLiteral("-") : task.deviceAlias);
    text += QStringLiteral("设备禁用: %1\n").arg(task.deviceDisabled ? QStringLiteral("是") : QStringLiteral("否"));
    text += QStringLiteral("状态: %1\n").arg(task.status);
    text += QStringLiteral("详情: %1\n").arg(task.detail);
    text += QStringLiteral("%1目录: %2\n").arg(isSourceTask(task) ? QStringLiteral("发送") : QStringLiteral("接收"), task.targetFolder);
    text += QStringLiteral("源端地址: %1\n").arg(task.linkReady ? task.link.endpoint : QStringLiteral("-"));
    text += QStringLiteral("Relay 地址: %1\n").arg(task.linkReady && task.link.hasRelay() ? task.link.relayEndpoint : QStringLiteral("-"));
    text += QStringLiteral("会话编号: %1\n").arg(task.linkReady ? task.link.sessionId : QStringLiteral("-"));
    text += QStringLiteral("本地大小: %1\n").arg(formatBytes(task.localBytes));
    text += QStringLiteral("全局大小: %1\n").arg(task.globalBytes > 0 ? formatBytes(task.globalBytes) : QStringLiteral("-"));
    text += QStringLiteral("接收总量: %1\n").arg(formatBytes(task.receivedBytes));
    text += QStringLiteral("发送总量: %1\n").arg(formatBytes(task.sentBytes));
    text += QStringLiteral("忽略规则: %1 条\n").arg(task.ignoreRules.size());
    text += QStringLiteral("已忽略项目: %1\n").arg(task.ignoredCount);
    text += QStringLiteral("\n");
    return text;
}

void MainWindow::updateTaskTraffic(SyncTask* task, quint64 receivedBytes, quint64 sentBytes)
{
    if (task == nullptr) {
        return;
    }
    const qint64 nowMs = QDateTime::currentDateTimeUtc().toMSecsSinceEpoch();
    const quint64 receivedDelta = receivedBytes >= task->lastTrafficReceivedBytes
        ? receivedBytes - task->lastTrafficReceivedBytes
        : 0;
    const quint64 sentDelta = sentBytes >= task->lastTrafficSentBytes
        ? sentBytes - task->lastTrafficSentBytes
        : 0;

    if (task->rateWindowStartedAtMs <= 0 || nowMs < task->rateWindowStartedAtMs) {
        task->rateWindowStartedAtMs = nowMs;
        task->rateWindowReceivedBytes = 0;
        task->rateWindowSentBytes = 0;
    }
    task->rateWindowReceivedBytes += receivedDelta;
    task->rateWindowSentBytes += sentDelta;

    const qint64 windowMs = nowMs - task->rateWindowStartedAtMs;
    if (windowMs >= 1000) {
        const quint64 receivedRate = quint64((task->rateWindowReceivedBytes * 1000) / quint64(windowMs));
        const quint64 sentRate = quint64((task->rateWindowSentBytes * 1000) / quint64(windowMs));
        auto smoothRate = [](quint64 previous, quint64 current) -> quint64 {
            if (previous == 0) {
                return current;
            }
            if (current == 0) {
                const quint64 decayed = quint64((previous * 7) / 10);
                return decayed < 1024 ? 0 : decayed;
            }
            return quint64((previous * 3 + current * 7) / 10);
        };
        task->currentReceivedRate = smoothRate(task->currentReceivedRate, receivedRate);
        task->currentSentRate = smoothRate(task->currentSentRate, sentRate);
        task->rateWindowStartedAtMs = nowMs;
        task->rateWindowReceivedBytes = 0;
        task->rateWindowSentBytes = 0;
    }
    task->receivedBytes = receivedBytes;
    task->sentBytes = sentBytes;
    task->lastTrafficReceivedBytes = receivedBytes;
    task->lastTrafficSentBytes = sentBytes;
    task->lastTrafficAtMs = nowMs;
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
    globalLogs.append(line);
    rebuildLogView();
}

void MainWindow::appendTaskLog(const QString& taskID, const QString& message)
{
    const SyncTask* task = taskByID(taskID);
    const QString taskName = task != nullptr ? task->name : taskID;
    const QString line = QStringLiteral("[%1] [%2] %3")
        .arg(QDateTime::currentDateTime().toString(Qt::ISODate), taskName, message);
    globalLogs.append(line);
    taskLogs[taskID].append(line);
    rebuildLogView();
}

QString MainWindow::diagnosticsText() const
{
    QString text;
    text += QStringLiteral("OneSync Win7 Qt 诊断日志\n");
    text += QStringLiteral("生成时间: %1\n").arg(QDateTime::currentDateTimeUtc().toString(Qt::ISODate));
    text += QStringLiteral("任务数量: %1\n\n").arg(tasks.size());
    for (const SyncTask& task : tasks) {
        text += taskDiagnosticsText(task);
        text += QStringLiteral("任务日志:\n");
        text += taskLogs.value(task.id).join(QStringLiteral("\n"));
        text += QStringLiteral("\n\n");
    }
    text += QStringLiteral("全部日志:\n");
    text += globalLogs.join(QStringLiteral("\n"));
    text += QStringLiteral("\n");
    return text;
}
