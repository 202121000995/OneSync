#pragma once

#include "SyncLink.h"

#include <QMap>
#include <QMainWindow>
#include <QStringList>

class QAction;
class QCloseEvent;
class QComboBox;
class QLabel;
class QMenu;
class QPushButton;
class QPlainTextEdit;
class QStackedWidget;
class QSystemTrayIcon;
class QTableWidget;
class QThread;
struct Endpoint;
class SourceConnector;
class TargetConnector;

class MainWindow : public QMainWindow
{
    Q_OBJECT

public:
    explicit MainWindow(QWidget* parent = nullptr);

private slots:
    void createTask();
    void addTask();
    void startSelectedTask();
    void pauseSelectedTask();
    void rescanSelectedTask();
    void editSelectedTask();
    void deleteSelectedTask();
    void showSelectedSourceLink();
    void renameSelectedDevice();
    void toggleSelectedDeviceDisabled();
    void testSelectedConnection();
    void exportDiagnostics();
    void exportSelectedTaskDiagnostics();
    void copySelectedTaskError();
    void copyVisibleLogs();
    void clearVisibleLogs();
    void kickSelectedDevice();
    void showFromTray();
    void quitFromTray();

private:
    struct SyncTask {
        enum Role {
            Source,
            Target
        };

        QString id;
        Role role = Target;
        QString name;
        QString deviceAlias;
        QString linkText;
        QString targetFolder;
        QString status;
        QString detail;
        QStringList ignoreRules;
        SyncLink link;
        bool linkReady = false;
        bool running = false;
        quint64 localBytes = 0;
        quint64 globalBytes = 0;
        quint64 receivedBytes = 0;
        quint64 sentBytes = 0;
        quint64 currentReceivedRate = 0;
        quint64 currentSentRate = 0;
        quint64 lastTrafficReceivedBytes = 0;
        quint64 lastTrafficSentBytes = 0;
        quint64 ignoredCount = 0;
        qint64 startedAtMs = 0;
        qint64 lastTrafficAtMs = 0;
        int localFiles = 0;
        int connectedDevices = 0;
        int totalDevices = 1;
        bool deviceDisabled = false;
    };

    void closeEvent(QCloseEvent* event) override;
    void buildUi();
    void setupTray();
    void loadTasks();
    void saveTasks() const;
    void refreshTaskTable();
    void refreshButtons();
    void refreshLogFilter();
    void rebuildLogView();
    void switchPage(int page);
    void refreshSecondaryPages();
    int selectedTaskIndex() const;
    int selectedTaskIndexFromTables() const;
    SyncTask* selectedTask();
    const SyncTask* selectedTask() const;
    SyncTask* taskByID(const QString& taskID);
    const SyncTask* taskByID(const QString& taskID) const;
    void setTaskStatus(const QString& taskID, const QString& status, const QString& detail = QString());
    bool parseTaskLink(SyncTask* task, QString* error);
    QString roleLabel(const SyncTask& task) const;
    QString statusLabel(const SyncTask& task) const;
    bool isSourceTask(const SyncTask& task) const;
    bool runTaskDialog(SyncTask* task, bool editing);
    bool runSourceTaskDialog(SyncTask* task, bool editing);
    QString buildSourceLink(const QString& relayEndpoint, const QString& relayToken, const QString& caCertificatePem, QString* error) const;
    bool testTaskConnection(const SyncTask& task, QString* detail) const;
    void showSourceLink(const SyncTask& task);
    void showTaskParameters(SyncTask* task);
    QString taskDiagnosticsText(const SyncTask& task) const;
    void updateTaskTraffic(SyncTask* task, quint64 receivedBytes, quint64 sentBytes);
    QString formatBytes(quint64 value) const;
    QString formatRate(quint64 value) const;
    void appendLog(const QString& message);
    void appendTaskLog(const QString& taskID, const QString& message);
    QString diagnosticsText() const;

    QTableWidget* taskTable = nullptr;
    QTableWidget* deviceTable = nullptr;
    QTableWidget* connectionTable = nullptr;
    QComboBox* logFilterCombo = nullptr;
    QPlainTextEdit* logEdit = nullptr;
    QPlainTextEdit* pageLogEdit = nullptr;
    QPushButton* startButton = nullptr;
    QPushButton* pauseButton = nullptr;
    QPushButton* rescanButton = nullptr;
    QPushButton* parametersButton = nullptr;
    QPushButton* deleteButton = nullptr;
    QPushButton* linkButton = nullptr;
    QLabel* pageTitleLabel = nullptr;
    QLabel* summaryLabel = nullptr;
    QStackedWidget* pages = nullptr;
    QList<QPushButton*> navButtons;
    QList<SyncTask> tasks;
    QMap<QString, QThread*> connectionThreads;
    QMap<QString, TargetConnector*> connectors;
    QMap<QString, SourceConnector*> sourceConnectors;
    QStringList globalLogs;
    QMap<QString, QStringList> taskLogs;
    QSystemTrayIcon* trayIcon = nullptr;
    QMenu* trayMenu = nullptr;
    bool exiting = false;
    bool trayCloseTipShown = false;
};
