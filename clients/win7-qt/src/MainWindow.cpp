#include "MainWindow.h"

#include "TargetConnector.h"

#include <QBoxLayout>
#include <QDateTime>
#include <QFile>
#include <QFileDialog>
#include <QFileInfo>
#include <QFormLayout>
#include <QGroupBox>
#include <QIODevice>
#include <QLabel>
#include <QLineEdit>
#include <QMessageBox>
#include <QPlainTextEdit>
#include <QPushButton>
#include <QStandardPaths>
#include <QThread>
#include <QTextEdit>

MainWindow::MainWindow(QWidget* parent)
    : QMainWindow(parent)
{
    setWindowTitle(QStringLiteral("OneSync Win7 兼容客户端"));
    resize(760, 560);

    auto* root = new QWidget(this);
    auto* layout = new QVBoxLayout(root);

    auto* intro = new QLabel(QStringLiteral("Win7 兼容客户端：当前先支持粘贴同步链接、选择目标目录和诊断导出。"));
    intro->setWordWrap(true);
    layout->addWidget(intro);

    auto* linkBox = new QGroupBox(QStringLiteral("加入同步"));
    auto* linkLayout = new QVBoxLayout(linkBox);
    linkEdit = new QTextEdit();
    linkEdit->setPlaceholderText(QStringLiteral("粘贴源端生成的同步链接"));
    linkEdit->setAcceptRichText(false);
    linkLayout->addWidget(linkEdit);

    auto* targetRow = new QHBoxLayout();
    targetFolderEdit = new QLineEdit();
    targetFolderEdit->setPlaceholderText(QStringLiteral("选择接收文件夹"));
    auto* chooseButton = new QPushButton(QStringLiteral("选择目录"));
    connect(chooseButton, &QPushButton::clicked, this, &MainWindow::chooseTargetFolder);
    targetRow->addWidget(targetFolderEdit);
    targetRow->addWidget(chooseButton);
    linkLayout->addLayout(targetRow);

    auto* actions = new QHBoxLayout();
    auto* parseButton = new QPushButton(QStringLiteral("解析链接"));
    startButton = new QPushButton(QStringLiteral("开始同步"));
    startButton->setEnabled(false);
    auto* diagnosticsButton = new QPushButton(QStringLiteral("导出诊断"));
    connect(parseButton, &QPushButton::clicked, this, &MainWindow::parseLink);
    connect(startButton, &QPushButton::clicked, this, &MainWindow::startSync);
    connect(diagnosticsButton, &QPushButton::clicked, this, &MainWindow::exportDiagnostics);
    actions->addWidget(parseButton);
    actions->addWidget(startButton);
    actions->addStretch(1);
    actions->addWidget(diagnosticsButton);
    linkLayout->addLayout(actions);
    layout->addWidget(linkBox);

    auto* summaryBox = new QGroupBox(QStringLiteral("链接信息"));
    auto* form = new QFormLayout(summaryBox);
    sessionLabel = new QLabel(QStringLiteral("-"));
    sourceEndpointLabel = new QLabel(QStringLiteral("-"));
    relayEndpointLabel = new QLabel(QStringLiteral("-"));
    expiresLabel = new QLabel(QStringLiteral("-"));
    statusLabel = new QLabel(QStringLiteral("未解析"));
    form->addRow(QStringLiteral("会话编号"), sessionLabel);
    form->addRow(QStringLiteral("源端地址"), sourceEndpointLabel);
    form->addRow(QStringLiteral("Relay 地址"), relayEndpointLabel);
    form->addRow(QStringLiteral("过期时间"), expiresLabel);
    form->addRow(QStringLiteral("状态"), statusLabel);
    layout->addWidget(summaryBox);

    logEdit = new QPlainTextEdit();
    logEdit->setReadOnly(true);
    logEdit->setPlaceholderText(QStringLiteral("运行日志会显示在这里。"));
    layout->addWidget(logEdit, 1);

    setCentralWidget(root);
    appendLog(QStringLiteral("OneSync Win7 Qt 客户端已启动。"));
}

void MainWindow::chooseTargetFolder()
{
    const QString folder = QFileDialog::getExistingDirectory(this, QStringLiteral("选择接收文件夹"));
    if (!folder.isEmpty()) {
        targetFolderEdit->setText(folder);
        appendLog(QStringLiteral("已选择接收目录：%1").arg(folder));
    }
}

void MainWindow::parseLink()
{
    SyncLink parsed;
    QString error;
    if (!SyncLinkParser::parse(linkEdit->toPlainText(), &parsed, &error)) {
        linkReady = false;
        startButton->setEnabled(false);
        statusLabel->setText(QStringLiteral("链接无效"));
        appendLog(QStringLiteral("解析失败：%1").arg(error));
        QMessageBox::warning(this, QStringLiteral("同步链接无效"), error);
        return;
    }
    currentLink = parsed;
    linkReady = true;
    startButton->setEnabled(true);
    updateLinkSummary(currentLink);
    appendLog(QStringLiteral("同步链接解析成功。"));
}

void MainWindow::startSync()
{
    if (!linkReady) {
        QMessageBox::warning(this, QStringLiteral("还不能开始"), QStringLiteral("请先解析同步链接。"));
        return;
    }
    const QString targetFolder = targetFolderEdit->text().trimmed();
    if (targetFolder.isEmpty()) {
        QMessageBox::warning(this, QStringLiteral("缺少接收目录"), QStringLiteral("请先选择接收文件夹。"));
        return;
    }
    const QFileInfo targetInfo(targetFolder);
    if (!targetInfo.exists() || !targetInfo.isDir()) {
        QMessageBox::warning(this, QStringLiteral("目录不可用"), QStringLiteral("接收文件夹不存在或不是目录。"));
        return;
    }
    if (connectionThread != nullptr) {
        QMessageBox::information(this, QStringLiteral("正在连接"), QStringLiteral("当前任务正在连接，请稍候。"));
        return;
    }

    startButton->setEnabled(false);
    statusLabel->setText(QStringLiteral("运行-连接中"));
    appendLog(QStringLiteral("开始连接源端。"));

    connectionThread = new QThread(this);
    auto* connector = new TargetConnector(currentLink, targetFolder);
    connector->moveToThread(connectionThread);

    connect(connectionThread, &QThread::started, connector, &TargetConnector::run);
    connect(connector, &TargetConnector::logMessage, this, &MainWindow::appendLog);
    connect(connector, &TargetConnector::statusChanged, statusLabel, &QLabel::setText);
    connect(connector, &TargetConnector::finished, this, [this](bool ok, const QString& message) {
        appendLog(message);
        statusLabel->setText(ok ? QStringLiteral("运行-已连接源端") : QStringLiteral("失败"));
        startButton->setEnabled(true);
        if (!ok) {
            QMessageBox::warning(this, QStringLiteral("连接失败"), message);
        } else {
            QMessageBox::information(this, QStringLiteral("连接成功"), message);
        }
    });
    connect(connector, &TargetConnector::finished, connectionThread, &QThread::quit);
    connect(connector, &TargetConnector::finished, connector, &TargetConnector::deleteLater);
    connect(connectionThread, &QThread::finished, connectionThread, &QThread::deleteLater);
    connect(connectionThread, &QThread::finished, this, [this]() {
        connectionThread = nullptr;
    });

    connectionThread->start();
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

void MainWindow::appendLog(const QString& message)
{
    const QString line = QStringLiteral("[%1] %2")
        .arg(QDateTime::currentDateTime().toString(Qt::ISODate))
        .arg(message);
    logEdit->appendPlainText(line);
}

void MainWindow::updateLinkSummary(const SyncLink& link)
{
    sessionLabel->setText(link.sessionId);
    sourceEndpointLabel->setText(link.endpoint);
    relayEndpointLabel->setText(link.hasRelay() ? link.relayEndpoint : QStringLiteral("未填写"));
    expiresLabel->setText(link.expiresAt.toLocalTime().toString(Qt::ISODate));
    statusLabel->setText(link.hasRelay() ? QStringLiteral("可通过 Relay 加入") : QStringLiteral("仅直连"));
}

QString MainWindow::diagnosticsText() const
{
    QString text;
    text += QStringLiteral("OneSync Win7 Qt 诊断日志\n");
    text += QStringLiteral("生成时间: %1\n").arg(QDateTime::currentDateTimeUtc().toString(Qt::ISODate));
    text += QStringLiteral("链接状态: %1\n").arg(linkReady ? QStringLiteral("已解析") : QStringLiteral("未解析"));
    text += QStringLiteral("接收目录: %1\n").arg(targetFolderEdit->text().trimmed());
    text += QStringLiteral("源端地址: %1\n").arg(sourceEndpointLabel->text());
    text += QStringLiteral("Relay 地址: %1\n").arg(relayEndpointLabel->text());
    text += QStringLiteral("\n运行日志:\n");
    text += logEdit->toPlainText();
    text += QStringLiteral("\n");
    return text;
}
