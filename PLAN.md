全程网页操作 不走配置文件yaml

1.  初始化javdb账号token

    优先读取环境变量，没有设置则手动输入，然后进行测试，确认可用后写入db(我用postgresql，开发时可以用sqlite进行测试)

2.  初始化清单

    一键读取当前所有清单，如果还未初始化，则可以一键创建多个清单（想看、想看中文，v1只需要支持想看，想看中文往后放，想看中文初步设想是有磁力就加入下载，但是不删除，直到有中文磁力才加入下载并删除）；如果发现已经有同名清单，则可以跳过创建；之后记录下这几个清单的id（不认清单名，而是认id）

3.  连接qbittorrent

    输入qbt地址，用户名和密码可选；设置创建下载任务的默认参数（分类cate）；自动设置qbittorrent下载完成后自动执行curl本项目的webhook（需要设置base url，比如我想通过mdc.raenzo.com访问本项目，但是qbt可以走集群内mac.mdc.svc.local）（需要兼容之前已经有其他webhook的情况，还需要幂等）

4.  设置jellyfin

    输入jellyfin地址和api key；

5.  设置mteam（可选，设置后优先从mteam下载）

    输入mteam的apikey

6.  设置ai翻译（可选）
7.  内部循环

    轮询“想看”清单，检查每个番号是否有磁力链接，将磁力加入qbt下载（设置cate，将番号作为tag，可以省去解析番号的过程，同时添加一个tag：mdc:in-progress(tag名暂定，可以改)标记这是本项目添加的，还未刮削），并将这个番号从想看清单中删除（必须先添加下载再删除，删除失败，下次重入再次添加下载会失败，跳过下载即可）

8.  qbt下载

    由于上面的设置，会在下载完成后自动通知本项目

9.  刮削

    接收到qbt的通知后（有小概率通知失败，本项目还需要定时检查是否有未处理的已经下载完成的任务（根据上面的tag mdc:in-progress）

    处理完成后需要将mdc:in-progress换成mdc:finished

    从tag中获取番号（获取失败才解析，v1不做解析）

    去多个网站爬起对应信息（图片用dmm家的，高清；标题、描述也用dmm的吧，其他家都是中文日文混杂，分不清，我们直接用最原始的，让ai翻译成中文；演员用javdb的，还能区分男优女优；其他的也都用javdb吧，图片爬来应该是一个横图，只能作为fanart，需要右侧裁剪得到poster，多个预览图作为extrafanart）

    根据刮削到的信息生成nfo文件，硬链接视频，下载并处理图片啥的

10. 生成一条任务(应该在检测到这个磁力时就生成)，主键是番号，和磁力的对应关系是一个番号可以对应多个磁力（中文 破解马赛克，这种特殊的再来就可以省去刮削，直接硬链接就好了（不同版本））
11. 通知jellyfin进行刷新媒体库
12. 通过telegram通知用户（下载开始，入库完成，刮削失败）
13. 用户可以手动重试刮削某个番号
14. vr视频一般分成cd1 cd2 cd3 要注意处理一下
15. 记录下演员，后面再做女优信息刮削
16. 轮询清单应该不用登录态就可以，只有删除时采用登录态，减少风控的风险

检查是否有磁力时：

优先检查javdb：javdb没有的就直接结束，javdb有的才去mteam检查，如果mteam有则优先下载mteam的（注意mteam不是磁力链接而是种子文件）

参考项目：/home/zhao/clone/open-mdcng(参考闭源mdcng项目写的，没实际用过) /home/zhao/clone/mdc-lite(open-mdcng前端不好看，又重新写的，也没实际用过) /home/zhao/clone/javdb_rss(这个有在使用)

/home/zhao/clone/jellyfin
/home/zhao/clone/qBittorrent
/home/zhao/clone/mihomo-headless-worker(我路由器上跑着mihomo，dmm会自动路由到日本出站，v1版本可以不管代理的情况)
前端页面我想弄好看点，去搜索一下开源项目，哪些有帮助
最终成品还是go 内嵌前端吧 前端用shadcn+tailwindcss 能用框架的就用框架

